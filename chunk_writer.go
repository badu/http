/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package http

import (
	"fmt"
	"io"
	"io/ioutil"
	"strconv" // TODO : get rid of it
	"time"

	"github.com/badu/http/hdr"
	"github.com/badu/http/sniff"
)

func (w *chunkWriter) Write(p []byte) (int, error) {
	if !w.wroteHeader {
		w.writeHeader(p)
	}
	if w.res.req.Method == HEAD {
		// Eat writes.
		return len(p), nil
	}
	if w.chunking {
		_, err := fmt.Fprintf(w.res.conn.bufWriter, "%x\r\n", len(p))
		if err != nil {
			w.res.conn.netConIface.Close()
			return 0, err
		}
	}
	n, err := w.res.conn.bufWriter.Write(p)
	if w.chunking && err == nil {
		_, err = w.res.conn.bufWriter.Write(CrLf)
	}
	if err != nil {
		w.res.conn.netConIface.Close()
	}
	return n, err
}

func (w *chunkWriter) flush() {
	if !w.wroteHeader {
		w.writeHeader(nil)
	}
	w.res.conn.bufWriter.Flush()
}

func (w *chunkWriter) close() {
	if !w.wroteHeader {
		w.writeHeader(nil)
	}
	if w.chunking {
		bw := w.res.conn.bufWriter // conn's bufio writer
		// zero chunk to mark EOF
		bw.WriteString("0\r\n")
		if trailers := w.res.finalTrailers(); trailers != nil {
			trailers.Write(bw) // the writer handles noting errors
		}
		// final blank line after the trailers (whether
		// present or not)
		bw.Write(CrLf)
	}
}

// writeHeader finalizes the header sent to the client and writes it
// to cw.res.conn.bufWriter.
//
// p is not written by writeHeader, but is the first chunk of the body
// that will be written. It is sniffed for a Content-Type if none is
// set explicitly. It's also used to set the Content-Length, if the
// total body size was small and the handler has already finished
// running.
func (w *chunkWriter) writeHeader(p []byte) {
	if w.wroteHeader {
		//TODO : @Badu - maybe, just maybe, some of these methods should return an error, so we don't silently discard behavior
		return
	}
	w.wroteHeader = true

	res := w.res
	keepAlivesEnabled := res.conn.server.doKeepAlives()
	isHEAD := res.req.Method == HEAD

	// header is written out to w.conn.buf below. Depending on the
	// state of the handler, we either own the map or not. If we
	// don't own it, the exclude map is created lazily for
	// WriteSubset to remove headers. The setHeader struct holds
	// headers we need to add.
	header := w.header
	owned := header != nil
	if !owned {
		header = res.handlerHeader
	}
	var excludeHeader map[string]bool
	delHeader := func(key string) {
		if owned {
			header.Del(key)
			return
		}
		if _, ok := header[key]; !ok {
			return
		}
		if excludeHeader == nil {
			excludeHeader = make(map[string]bool)
		}
		excludeHeader[key] = true
	}
	var setHeader extraHeader

	// Don't write out the fake "Trailer:foo" keys. See TrailerPrefix.
	trailers := false
	for k := range w.header {
		//@comment : was `if strings.HasPrefix(k, TrailerPrefix) {`
		if len(k) >= 8 && k[:8] == TrailerPrefix {
			if excludeHeader == nil {
				excludeHeader = make(map[string]bool)
			}
			excludeHeader[k] = true
			trailers = true
		}
	}
	for _, v := range w.header[hdr.Trailer] {
		trailers = true
		foreachHeaderElement(v, w.res.declareTrailer)
	}

	te := header.Get(hdr.TransferEncoding)
	hasTE := te != ""

	// If the handler is done but never sent a Content-Length
	// response header and this is our first (and last) write, set
	// it, even to zero. This helps HTTP/1.0 clients keep their
	// "keep-alive" connections alive.
	// Exceptions: 304/204/1xx responses never get Content-Length, and if
	// it was a HEAD request, we don't know the difference between
	// 0 actual bytes and 0 bytes because the handler noticed it
	// was a HEAD request and chose not to write anything. So for
	// HEAD, the handler should either write the Content-Length or
	// write non-zero bytes. If it's actually 0 bytes and the
	// handler never looked at the Request.Method, we just don't
	// send a Content-Length header.
	// Further, we don't send an automatic Content-Length if they
	// set a Transfer-Encoding, because they're generally incompatible.
	if res.handlerDone.isSet() && !trailers && !hasTE && bodyAllowedForStatus(res.status) && header.Get(hdr.ContentLength) == "" && (!isHEAD || len(p) > 0) {
		res.contentLength = int64(len(p))
		setHeader.contentLength = strconv.AppendInt(w.res.clenBuf[:0], int64(len(p)), 10)
	}

	// If this was an HTTP/1.0 request with keep-alive and we sent a
	// Content-Length back, we can make this a keep-alive response ...
	if res.wants10KeepAlive && keepAlivesEnabled {
		sentLength := header.Get(hdr.ContentLength) != ""
		if sentLength && header.Get(hdr.Connection) == DoKeepAlive {
			res.closeAfterReply = false
		}
	}

	// Check for a explicit (and valid) Content-Length header.
	hasCL := res.contentLength != -1

	if res.wants10KeepAlive && (isHEAD || hasCL || !bodyAllowedForStatus(res.status)) {
		_, connectionHeaderSet := header[hdr.Connection]
		if !connectionHeaderSet {
			setHeader.connection = DoKeepAlive
		}
	} else if !res.req.ProtoAtLeast(1, 1) || res.wantsClose {
		res.closeAfterReply = true
	}

	if header.Get(hdr.Connection) == DoClose || !keepAlivesEnabled {
		res.closeAfterReply = true
	}

	// If the client wanted a 100-continue but we never sent it to
	// them (or, more strictly: we never finished reading their
	// request body), don't reuse this connection because it's now
	// in an unknown state: we might be sending this response at
	// the same time the client is now sending its request body
	// after a timeout.  (Some HTTP clients send Expect:
	// 100-continue but knowing that some servers don't support
	// it, the clients set a timer and send the body later anyway)
	// If we haven't seen EOF, we can't skip over the unread body
	// because we don't know if the next bytes on the wire will be
	// the body-following-the-timer or the subsequent request.
	// See Issue 11549.
	if ecr, ok := res.req.Body.(*expectContinueReader); ok && !ecr.sawEOF {
		res.closeAfterReply = true
	}

	// Per RFC 2616, we should consume the request body before
	// replying, if the handler hasn't already done so. But we
	// don't want to do an unbounded amount of reading here for
	// DoS reasons, so we only try up to a threshold.
	// TODO(bradfitz): where does RFC 2616 say that? See Issue 15527
	// about HTTP/1.x Handlers concurrently reading and writing, like
	// HTTP/2 handlers can do. Maybe this code should be relaxed?
	if res.req.ContentLength != 0 && !res.closeAfterReply {
		var discard, tooBig bool

		switch bdy := res.req.Body.(type) {
		case *expectContinueReader:
			if bdy.resp.wroteContinue {
				discard = true
			}
		case *body:
			bdy.mu.Lock()
			switch {
			case bdy.isClosed:
				if !bdy.hasSawEOF {
					// Body was closed in handler with non-EOF error.
					res.closeAfterReply = true
				}
			case bdy.unreadDataSizeLocked() >= maxPostHandlerReadBytes:
				tooBig = true
			default:
				discard = true
			}
			bdy.mu.Unlock()
		default:
			discard = true
		}

		if discard {
			_, err := io.CopyN(ioutil.Discard, res.reqBody, maxPostHandlerReadBytes+1)
			switch err {
			case nil:
				// There must be even more data left over.
				tooBig = true
			case ErrBodyReadAfterClose:
				// Body was already consumed and closed.
			case io.EOF:
				// The remaining body was just consumed, close it.
				err = res.reqBody.Close()
				if err != nil {
					res.closeAfterReply = true
				}
			default:
				// Some other kind of error occurred, like a read timeout, or
				// corrupt chunked encoding. In any case, whatever remains
				// on the wire must not be parsed as another HTTP request.
				res.closeAfterReply = true
			}
		}

		if tooBig {
			res.requestTooLarge()
			delHeader(hdr.Connection)
			setHeader.connection = DoClose
		}
	}

	code := res.status
	if bodyAllowedForStatus(code) {
		// If no content type, apply sniffing algorithm to body.
		_, haveType := header[hdr.ContentType]
		if !haveType && !hasTE {
			setHeader.contentType = sniff.DetectContentType(p)
		}
	} else {
		for _, k := range suppressedHeaders(code) {
			delHeader(k)
		}
	}

	if _, ok := header[hdr.Date]; !ok {
		setHeader.date = appendTime(w.res.dateBuf[:0], time.Now())
	}

	if hasCL && hasTE && te != DoIdentity {
		// TODO: return an error if WriteHeader gets a return parameter
		// For now just ignore the Content-Length.
		res.conn.server.logf("http: WriteHeader called with both Transfer-Encoding of %q and a Content-Length of %d", te, res.contentLength)
		delHeader(hdr.ContentLength)
		hasCL = false
	}

	if res.req.Method == HEAD || !bodyAllowedForStatus(code) {
		// do nothing
	} else if code == StatusNoContent {
		delHeader(hdr.TransferEncoding)
	} else if hasCL {
		delHeader(hdr.TransferEncoding)
	} else if res.req.ProtoAtLeast(1, 1) {
		// HTTP/1.1 or greater: Transfer-Encoding has been set to identity,  and no
		// content-length has been provided. The connection must be closed after the
		// reply is written, and no chunking is to be done. This is the setup
		// recommended in the Server-Sent Events candidate recommendation 11,
		// section 8.
		if hasTE && te == DoIdentity {
			w.chunking = false
			res.closeAfterReply = true
		} else {
			// HTTP/1.1 or greater: use chunked transfer encoding
			// to avoid closing the connection at EOF.
			w.chunking = true
			setHeader.transferEncoding = DoChunked
			if hasTE && te == DoChunked {
				// We will send the chunked Transfer-Encoding header later.
				delHeader(hdr.TransferEncoding)
			}
		}
	} else {
		// HTTP version < 1.1: cannot do chunked transfer
		// encoding and we don't know the Content-Length so
		// signal EOF by closing connection.
		res.closeAfterReply = true
		delHeader(hdr.TransferEncoding) // in case already set
	}

	// Cannot use Content-Length with non-identity Transfer-Encoding.
	if w.chunking {
		delHeader(hdr.ContentLength)
	}
	if !res.req.ProtoAtLeast(1, 0) {
		return
	}

	if res.closeAfterReply && (!keepAlivesEnabled || !hasToken(w.header.Get(hdr.Connection), DoClose)) {
		delHeader(hdr.Connection)
		if res.req.ProtoAtLeast(1, 1) {
			setHeader.connection = DoClose
		}
	}

	writeStatusLine(res.conn.bufWriter, res.req.ProtoAtLeast(1, 1), code, res.statusBuf[:])
	w.header.WriteSubset(res.conn.bufWriter, excludeHeader)
	setHeader.Write(res.conn.bufWriter)
	res.conn.bufWriter.Write(CrLf)
}
