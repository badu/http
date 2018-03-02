/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package http

import (
	"bufio"
	"io"
	"net"
	"strconv"
)

// finalTrailers is called after the Handler exits and returns a non-nil
// value if the Handler set any trailers.
func (r *response) finalTrailers() Header {
	var t Header
	for k, vv := range r.handlerHeader {
		//@comment : was `if strings.HasPrefix(k, TrailerPrefix) {`
		if len(k) >= len(TrailerPrefix) && k[0:len(TrailerPrefix)] == TrailerPrefix {
			if t == nil {
				t = make(Header)
			}
			//@comment : was `t[strings.TrimPrefix(k, TrailerPrefix)] = vv`
			t[k[len(TrailerPrefix):]] = vv
		}
	}
	for _, k := range r.trailers {
		if t == nil {
			t = make(Header)
		}
		for _, v := range r.handlerHeader[k] {
			t.Add(k, v)
		}
	}
	return t
}

// declareTrailer is called for each Trailer header when the
// response header is written. It notes that a header will need to be
// written in the trailers at the end of the response.
func (r *response) declareTrailer(k string) {
	k = CanonicalHeaderKey(k)
	switch k {
	case TransferEncoding, ContentLength, Trailer:
		// Forbidden by RFC 2616 14.40.
		return
	}
	r.trailers = append(r.trailers, k)
}

// requestTooLarge is called by maxBytesReader when too much input has
// been read from the client.
func (r *response) requestTooLarge() {
	r.closeAfterReply = true
	r.requestBodyLimitHit = true
	if !r.wroteHeader {
		r.Header().Set(Connection, DoClose)
	}
}

// needsSniff reports whether a Content-Type still needs to be sniffed.
func (r *response) needsSniff() bool {
	_, haveType := r.handlerHeader[ContentType]
	return !r.chunkWriter.wroteHeader && !haveType && r.written < SniffLen
}

// ReadFrom is here to optimize copying from an *os.File regular file
// to a *net.TCPConn with sendfile.
func (r *response) ReadFrom(src io.Reader) (int64, error) {
	// Our underlying r.conn.netConIface is usually a *TCPConn (with its
	// own ReadFrom method). If not, or if our src isn't a regular
	// file, just fall back to the normal copy method.
	rf, ok := r.conn.netConIface.(io.ReaderFrom)
	regFile, err := srcIsRegularFile(src)
	if err != nil {
		return 0, err
	}
	if !ok || !regFile {
		bufp := copyBufPool.Get().(*[]byte)
		defer copyBufPool.Put(bufp)
		return io.CopyBuffer(writerOnly{r}, src, *bufp)
	}

	// sendfile path:

	if !r.wroteHeader {
		r.WriteHeader(StatusOK)
	}

	n := int64(0)
	if r.needsSniff() {
		n0, err := io.Copy(writerOnly{r}, io.LimitReader(src, SniffLen))
		n += n0
		if err != nil {
			return n, err
		}
	}

	r.bufWriter.Flush()   // get rid of any previous writes
	r.chunkWriter.flush() // make sure Header is written; flush data to netConIface

	// Now that cw has been flushed, its chunking field is guaranteed initialized.
	if !r.chunkWriter.chunking && r.bodyAllowed() {
		n0, err := rf.ReadFrom(src)
		n += n0
		r.written += n0
		return n, err
	}

	n0, err := io.Copy(writerOnly{r}, src)
	n += n0
	return n, err
}

func (r *response) Header() Header {
	if r.chunkWriter.header == nil && r.wroteHeader && !r.chunkWriter.wroteHeader {
		// Accessing the header between logically writing it
		// and physically writing it means we need to allocate
		// a clone to snapshot the logically written state.
		r.chunkWriter.header = r.handlerHeader.Clone()
	}
	r.calledHeader = true
	return r.handlerHeader
}

func (r *response) WriteHeader(code int) {
	if r.conn.hijacked() {
		r.conn.server.logf("http: response.WriteHeader on hijacked connection")
		return
	}
	if r.wroteHeader {
		r.conn.server.logf("http: multiple response.WriteHeader calls")
		return
	}
	r.wroteHeader = true
	r.status = code

	if r.calledHeader && r.chunkWriter.header == nil {
		r.chunkWriter.header = r.handlerHeader.Clone()
	}

	if cl := r.handlerHeader.get(ContentLength); cl != "" {
		v, err := strconv.ParseInt(cl, 10, 64)
		if err == nil && v >= 0 {
			r.contentLength = v
		} else {
			r.conn.server.logf("http: invalid Content-Length of %q", cl)
			r.handlerHeader.Del(ContentLength)
		}
	}
}

// bodyAllowed reports whether a Write is allowed for this response type.
// It's illegal to call this before the header has been flushed.
func (r *response) bodyAllowed() bool {
	if !r.wroteHeader {
		panic("")
	}
	return bodyAllowedForStatus(r.status)
}

// The Life Of A Write is like this:
//
// Handler starts. No header has been sent. The handler can either
// write a header, or just start writing. Writing before sending a header
// sends an implicitly empty 200 OK header.
//
// If the handler didn't declare a Content-Length up front, we either
// go into chunking mode or, if the handler finishes running before
// the chunking buffer size, we compute a Content-Length and send that
// in the header instead.
//
// Likewise, if the handler didn't set a Content-Type, we sniff that
// from the initial chunk of output.
//
// The Writers are wired together like:
//
// 1. *response (the ResponseWriter) ->
// 2. (*response).w, a *bufio.Writer of bufferBeforeChunkingSize bytes
// 3. chunkWriter.Writer (whose writeHeader finalizes Content-Length/Type)
//    and which writes the chunk headers, if needed.
// 4. conn.buf, a bufio.Writer of default (4kB) bytes, writing to ->
// 5. checkConnErrorWriter{c}, which notes any non-nil error on Write
//    and populates c.wErr with it if so. but otherwise writes to:
// 6. the netConIface, the net.Conn.
//
// TODO(bradfitz): short-circuit some of the buffering when the initial header contains both a Content-Type and Content-Length. Also short-circuit in (1) when the header's been sent and not in chunking mode, writing directly to (4) instead, if (2) has no buffered data. More generally, we could short-circuit from (1) to (3) even in chunking mode if the write size from (1) is over some threshold and nothing is in (2).  The answer might be mostly making bufferBeforeChunkingSize smaller and having bufio's fast-paths deal with this instead.
func (r *response) Write(data []byte) (int, error) {
	lenData := len(data)
	if r.conn.hijacked() {
		if lenData > 0 {
			r.conn.server.logf("http: response.Write on hijacked connection")
		}
		return 0, ErrHijacked
	}
	if !r.wroteHeader {
		r.WriteHeader(StatusOK)
	}
	if lenData == 0 {
		return 0, nil
	}
	if !r.bodyAllowed() {
		return 0, ErrBodyNotAllowed
	}

	r.written += int64(lenData) // ignoring errors, for errorKludge
	if r.contentLength != -1 && r.written > r.contentLength {
		return 0, ErrContentLength
	}
	return r.bufWriter.Write(data)
}

func (r *response) WriteString(data string) (int, error) {
	lenData := len(data)
	if r.conn.hijacked() {
		if lenData > 0 {
			r.conn.server.logf("http: response.Write on hijacked connection")
		}
		return 0, ErrHijacked
	}
	if !r.wroteHeader {
		r.WriteHeader(StatusOK)
	}
	if lenData == 0 {
		return 0, nil
	}
	if !r.bodyAllowed() {
		return 0, ErrBodyNotAllowed
	}

	r.written += int64(lenData) // ignoring errors, for errorKludge
	if r.contentLength != -1 && r.written > r.contentLength {
		return 0, ErrContentLength
	}
	return r.bufWriter.WriteString(data)
}

func (r *response) finishRequest() {
	r.handlerDone.setTrue()

	if !r.wroteHeader {
		r.WriteHeader(StatusOK)
	}

	r.bufWriter.Flush()
	putBufioWriter(r.bufWriter)
	r.chunkWriter.close()
	r.conn.bufWriter.Flush()

	r.conn.reader.abortPendingRead()

	// Close the body (regardless of r.closeAfterReply) so we can
	// re-use its bufio.Reader later safely.
	r.reqBody.Close()

	if r.req.MultipartForm != nil {
		r.req.MultipartForm.RemoveAll()
	}
}

// shouldReuseConnection reports whether the underlying TCP connection can be reused.
// It must only be called after the handler is done executing.
func (r *response) shouldReuseConnection() bool {
	if r.closeAfterReply {
		// The request or something set while executing the
		// handler indicated we shouldn't reuse this
		// connection.
		return false
	}

	if r.req.Method != HEAD && r.contentLength != -1 && r.bodyAllowed() && r.contentLength != r.written {
		// Did not write enough. Avoid getting out of sync.
		return false
	}

	// There was some error writing to the underlying connection
	// during the request, so don't re-use this conn.
	if r.conn.wErr != nil {
		return false
	}

	if r.closedRequestBodyEarly() {
		return false
	}

	return true
}

func (r *response) closedRequestBodyEarly() bool {
	body, ok := r.req.Body.(*body)
	return ok && body.didEarlyClose()
}

func (r *response) Flush() {
	if !r.wroteHeader {
		r.WriteHeader(StatusOK)
	}
	r.bufWriter.Flush()
	r.chunkWriter.flush()
}

func (r *response) sendExpectationFailed() {
	// TODO(bradfitz): let ServeHTTP handlers handle requests with non-standard expectation[s]? Seems theoretical at best, and doesn't fit into the current ServeHTTP model anyway. We'd need to make the ResponseWriter an optional "ExpectReplier" interface or something.
	//
	// For now we'll just obey RFC 2616 14.20 which says
	// "If a server receives a request containing an
	// Expect field that includes an expectation-
	// extension that it does not support, it MUST
	// respond with a 417 (Expectation Failed) status."
	r.Header().Set(Connection, DoClose)
	r.WriteHeader(StatusExpectationFailed)
	r.finishRequest()
}

// Hijack implements the Hijacker.Hijack method. Our response is both a ResponseWriter
// and a Hijacker.
func (r *response) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if r.handlerDone.isSet() {
		panic("github.com/badu//http: Hijack called after ServeHTTP finished")
	}
	if r.wroteHeader {
		r.chunkWriter.flush()
	}

	c := r.conn
	c.mu.Lock()
	defer c.mu.Unlock()

	// Release the bufioWriter that writes to the chunk writer, it is not
	// used after a connection has been hijacked.
	rwc, buf, err := c.hijackLocked()
	if err == nil {
		putBufioWriter(r.bufWriter)
		r.bufWriter = nil
	}
	return rwc, buf, err
}

func (r *response) CloseNotify() <-chan bool {
	if r.handlerDone.isSet() {
		panic("github.com/badu//http: CloseNotify called after ServeHTTP finished")
	}
	return r.closeNotifyCh
}
