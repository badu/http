/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package http

import (
	"errors"
	"io"
	"io/ioutil"

	"github.com/badu/http/hdr"
)

func (b *body) Read(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.isClosed {
		return 0, ErrBodyReadAfterClose
	}
	return b.readLocked(p)
}

// Must hold b.mu.
func (b *body) readLocked(p []byte) (int, error) {
	if b.hasSawEOF {
		return 0, io.EOF
	}
	n, err := b.reader.Read(p)
	if err == io.EOF {
		b.hasSawEOF = true
		// Chunked case. Read the trailer.
		if b.responseOrRequestIntf != nil {
			if e := b.readTrailer(); e != nil {
				err = e
				// Something went wrong in the trailer, we must not allow any
				// further reads of any kind to succeed from body, nor any
				// subsequent requests on the server connection. See
				// golang.org/issue/12027
				b.hasSawEOF = false
				b.isClosed = true
			}
			b.responseOrRequestIntf = nil
		} else {
			// If the server declared the Content-Length, our body is a LimitedReader
			// and we need to check whether this EOF arrived early.
			if lr, ok := b.reader.(*io.LimitedReader); ok && lr.N > 0 {
				err = io.ErrUnexpectedEOF
			}
		}
	}

	// If we can return an EOF here along with the read data, do
	// so. This is optional per the io.Reader contract, but doing
	// so helps the HTTP transport code recycle its connection
	// earlier (since it will see this EOF itself), even if the
	// client doesn't do future reads or Close.
	if err == nil && n > 0 {
		if lr, ok := b.reader.(*io.LimitedReader); ok && lr.N == 0 {
			err = io.EOF
			b.hasSawEOF = true
		}
	}

	if b.hasSawEOF && b.onHitEOF != nil {
		b.onHitEOF()
	}

	return n, err
}

func (b *body) readTrailer() error {
	// The common case, since nobody uses trailers.
	buf, err := b.bufReader.Peek(2)
	if equal(buf, CrLf) {
		b.bufReader.Discard(2)
		return nil
	}
	if len(buf) < 2 {
		return errTrailerEOF
	}
	if err != nil {
		return err
	}

	// Make sure there's a header terminator coming up, to prevent
	// a DoS with an unbounded size Trailer. It's not easy to
	// slip in a LimitReader here, as NewHeaderReader requires
	// a concrete *bufio.Reader. Also, we can't get all the way
	// back up to our conn's LimitedReader that *might* be backing
	// this bufio.Reader. Instead, a hack: we iteratively Peek up
	// to the bufio.Reader's max size, looking for a double CRLF.
	// This limits the trailer to the underlying buffer size, typically 4kB.
	if !seeUpcomingDoubleCRLF(b.bufReader) {
		return errors.New("http: suspiciously long trailer after chunked body")
	}

	header, err := hdr.NewHeaderReader(b.bufReader).ReadHeader()
	if err != nil {
		if err == io.EOF {
			return errTrailerEOF
		}
		return err
	}
	switch rr := b.responseOrRequestIntf.(type) {
	case *Request:
		mergeSetHeader(&rr.Trailer, hdr.Header(header))
	case *Response:
		mergeSetHeader(&rr.Trailer, hdr.Header(header))
	}
	return nil
}

// unreadDataSizeLocked returns the number of bytes of unread input.
// It returns -1 if unknown.
// b.mu must be held.
func (b *body) unreadDataSizeLocked() int64 {
	if lr, ok := b.reader.(*io.LimitedReader); ok {
		return lr.N
	}
	return -1
}

func (b *body) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.isClosed {
		return nil
	}
	var err error
	switch {
	case b.hasSawEOF:
		// Already saw EOF, so no need going to look for it.
	case b.responseOrRequestIntf == nil && b.isClosing:
		// no trailer and closing the connection next.
		// no point in reading to EOF.
	case b.doEarlyClose:
		// Read up to maxPostHandlerReadBytes bytes of the body, looking for
		// for EOF (and trailers), so we can re-use this connection.
		if lr, ok := b.reader.(*io.LimitedReader); ok && lr.N > maxPostHandlerReadBytes {
			// There was a declared Content-Length, and we have more bytes remaining
			// than our maxPostHandlerReadBytes tolerance. So, give up.
			b.isEarlyClose = true
		} else {
			var n int64
			// Consume the body, or, which will also lead to us reading
			// the trailer headers after the body, if present.
			n, err = io.CopyN(ioutil.Discard, bodyLocked{b}, maxPostHandlerReadBytes)
			if err == io.EOF {
				err = nil
			}
			if n == maxPostHandlerReadBytes {
				b.isEarlyClose = true
			}
		}
	default:
		// Fully consume the body, which will also lead to us reading
		// the trailer headers after the body, if present.
		_, err = io.Copy(ioutil.Discard, bodyLocked{b})
	}
	b.isClosed = true
	return err
}

func (b *body) didEarlyClose() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.isEarlyClose
}

// bodyRemains reports whether future Read calls might yield data.
func (b *body) bodyRemains() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return !b.hasSawEOF
}

func (b *body) registerOnHitEOF(fn func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.onHitEOF = fn
}
