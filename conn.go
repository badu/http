/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package http

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"runtime"
	"time"

	"github.com/badu/http/hdr"
	"github.com/badu/http/url"
)

func (c *conn) hijacked() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.wasHijacked
}

// c.mu must be held.
func (c *conn) hijackLocked(ctx context.Context) (net.Conn, *bufio.ReadWriter, error) {
	if c.wasHijacked {
		return nil, nil, ErrHijacked
	}

	c.reader.abortPendingRead()

	c.wasHijacked = true

	netConn := c.netConIface
	netConn.SetDeadline(time.Time{})

	buf := bufio.NewReadWriter(c.bufReader, bufio.NewWriter(netConn))
	if c.reader.hasByte {
		if _, err := c.bufReader.Peek(c.bufReader.Buffered() + 1); err != nil {
			return nil, nil, fmt.Errorf("unexpected Peek failure reading buffered byte: %v", err)
		}
	}
	srv := ctx.Value(SrvCtxtKey).(*Server)
	srv.setState(c, StateHijacked)
	return netConn, buf, nil
}

// Read next request from connection.
func (c *conn) readRequest(ctx context.Context) (*response, error) {
	if c.hijacked() {
		return nil, ErrHijacked
	}

	var hdrDeadline time.Time // or zero if none
	t0 := time.Now()

	srv := ctx.Value(SrvCtxtKey).(*Server)

	if d := srv.readHeaderTimeout(); d != 0 {
		hdrDeadline = t0.Add(d)
	}

	var wholeReqDeadline time.Time // or zero if none
	if d := srv.ReadTimeout; d != 0 {
		wholeReqDeadline = t0.Add(d)
	}

	c.netConIface.SetReadDeadline(hdrDeadline)

	if d := srv.WriteTimeout; d != 0 {
		defer func() {
			c.netConIface.SetWriteDeadline(time.Now().Add(d))
		}()
	}

	c.reader.setReadLimit(srv.initialReadLimitSize())

	// RFC 2616 section 4.1 tolerance for old buggy clients.
	if c.lastMethod == POST {
		peek, _ := c.bufReader.Peek(4) // ReadRequest will get err below
		c.bufReader.Discard(numLeadingCRorLF(peek))
	}

	// @comment : reads info from the request (using textproto.Reader transforms bytes into textproto.MIMEHeader and other usefull info)
	req, err := readRequest(c.bufReader, false)
	if err != nil {
		if c.reader.hitReadLimit() {
			return nil, errTooLarge
		}
		return nil, err
	}

	if !http1ServerSupportsRequest(req) {
		//TODO : @badu - document
		return nil, badRequestError("unsupported protocol version")
	}

	c.lastMethod = req.Method
	c.reader.setInfiniteReadLimit()

	hosts, haveHost := req.Header[hdr.Host]
	if req.ProtoAtLeast(1, 1) && (!haveHost || len(hosts) == 0) && req.Method != CONNECT {
		//TODO : @badu - document
		return nil, badRequestError("missing required Host header")
	}
	if len(hosts) > 1 {
		//TODO : @badu - document
		return nil, badRequestError("too many Host headers")
	}
	if len(hosts) == 1 && !url.ValidHostHeader(hosts[0]) {
		//TODO : @badu - document
		return nil, badRequestError("malformed Host header")
	}
	for k, vv := range req.Header {
		if !hdr.ValidHeaderFieldName(k) {
			//TODO : @badu - document
			return nil, badRequestError("invalid header name")
		}
		for _, v := range vv {
			if !hdr.ValidHeaderFieldValue(v) {
				//TODO : @badu - document
				return nil, badRequestError("invalid header value")
			}
		}
	}
	delete(req.Header, hdr.Host)

	ctx, cancelCtx := context.WithCancel(ctx)
	req.ctx = ctx
	req.RemoteAddr = c.netConIface.RemoteAddr().String()
	req.TLS = c.tlsState
	if body, ok := req.Body.(*body); ok {
		body.doEarlyClose = true
	}

	// Adjust the read deadline if necessary.
	if !hdrDeadline.Equal(wholeReqDeadline) {
		c.netConIface.SetReadDeadline(wholeReqDeadline)
	}

	w := &response{
		conn:          c,
		ctx:           ctx,
		cancelCtx:     cancelCtx,
		req:           req,
		reqBody:       req.Body,
		handlerHeader: make(hdr.Header),
		contentLength: -1,
		closeNotifyCh: make(chan bool, 1),

		// We populate these ahead of time so we're not
		// reading from req.Header after their Handler starts
		// and maybe mutates it (Issue 14940)
		wants10KeepAlive: req.wantsHttp10KeepAlive(),
		wantsClose:       req.wantsClose(),
	}
	w.chunkWriter.res = w
	w.bufWriter = newBufioWriterSize(&w.chunkWriter, bufferBeforeChunkingSize)
	return w, nil
}

func (c *conn) finalFlush() {
	if c.bufReader != nil {
		// Steal the bufio.Reader (~4KB worth of memory) and its associated
		// reader for a future connection.
		putBufioReader(c.bufReader)
		c.bufReader = nil
	}

	if c.bufWriter != nil {
		c.bufWriter.Flush()
		// Steal the bufio.Writer (~4KB worth of memory) and its associated
		// writer for a future connection.
		putBufioWriter(c.bufWriter)
		c.bufWriter = nil
	}
}

// Close the connection.
func (c *conn) close() {
	c.finalFlush()
	c.netConIface.Close()
}

// closeWrite flushes any outstanding data and sends a FIN packet (if
// client is connected via TCP), signalling that we're done. We then
// pause for a bit, hoping the client processes it before any
// subsequent RST.
//
// See https://golang.org/issue/3595
func (c *conn) closeWriteAndWait() {
	c.finalFlush()
	if tcp, ok := c.netConIface.(closeWriter); ok {
		tcp.CloseWrite()
	}
	time.Sleep(rstAvoidanceDelay)
}

// Serve a new connection.
//TODO : @badu - maybe this should return error???
func (c *conn) serve(ctx context.Context) {

	// TODO : @badu - what if nil?
	srv := ctx.Value(SrvCtxtKey).(*Server)
	ctx = context.WithValue(ctx, LocalAddrContextKey, c.netConIface.LocalAddr())
	defer func() {
		// @comment : recovering from panic
		if err := recover(); err != nil && err != ErrAbortHandler {
			const size = 64 << 10
			buf := make([]byte, size)
			buf = buf[:runtime.Stack(buf, false)]
			srv.logf("http: panic serving %v: %v\n%s", c.netConIface.RemoteAddr().String(), err, buf)
		}
		// @comment :close non hijacked
		if !c.hijacked() {
			c.close()
			srv.setState(c, StateClosed)
		}
	}()

	// TODO : @badu - we should know earlier to handle tls.Conn
	if tlsConn, ok := c.netConIface.(*tls.Conn); ok {
		if d := srv.ReadTimeout; d != 0 {
			c.netConIface.SetReadDeadline(time.Now().Add(d))
		}
		if d := srv.WriteTimeout; d != 0 {
			c.netConIface.SetWriteDeadline(time.Now().Add(d))
		}
		if err := tlsConn.Handshake(); err != nil {
			srv.logf("http: TLS handshake error from %s: %v", c.netConIface.RemoteAddr(), err)
			return
		}
		// TODO : @badu - what an ugly way to clone
		c.tlsState = new(tls.ConnectionState)
		*c.tlsState = tlsConn.ConnectionState()
		if proto := c.tlsState.NegotiatedProtocol; validNPN(proto) {
			if fn := srv.TLSNextProto[proto]; fn != nil {
				h := initNPNRequest{tlsConn, serverHandler{srv}}
				fn(tlsConn, h)
			}
			return
		}
	}

	// HTTP/1.x from here on.
	ctx, cancelCtx := context.WithCancel(ctx)
	c.cancelCtx = cancelCtx
	defer cancelCtx()

	c.reader = &connReader{conn: c}
	c.bufReader = newBufioReader(c.reader)
	c.bufWriter = newBufioWriterSize(checkConnErrorWriter{c}, 4<<10) //TODO : @badu - this should be configurable - this is about bufioWriter4kPool

	for {
		// @comment : starts to read request
		resp, err := c.readRequest(ctx)
		if c.reader.remain != srv.initialReadLimitSize() {
			// If we read any bytes off the wire, we're active.
			srv.setState(c, StateActive)
		}
		if err != nil {
			if err == errTooLarge {
				// Their HTTP client may or may not be
				// able to read this if we're
				// responding to them and hanging up
				// while they're still writing their
				// request. Undefined behavior.
				fmt.Fprintf(c.netConIface, "HTTP/1.1 431 Request Header Fields Too Large"+errorHeaders+"431 Request Header Fields Too Large")
				c.closeWriteAndWait()
				return
			}
			if isCommonNetReadError(err) {
				return // don't reply
			}

			publicErr := "400 Bad Request"

			//TODO : @badu -Don’t assert errors for type, assert for behaviour.
			if v, ok := err.(badRequestError); ok {
				// //TODO : @badu - document - if conn.readRequest returned an error, it's typed badRequestError
				publicErr = publicErr + ": " + string(v)
			}

			fmt.Fprintf(c.netConIface, "HTTP/1.1 "+publicErr+errorHeaders+publicErr)
			return
		}

		// Expect 100 Continue support
		req := resp.req
		if req.ExpectsContinue() {
			if req.ProtoAtLeast(1, 1) && req.ContentLength != 0 {
				// Wrap the Body reader with one that replies on the connection
				req.Body = &expectContinueReader{readCloser: req.Body, resp: resp}
			}
		} else if req.Header.Get(hdr.Expect) != "" {
			resp.sendExpectationFailed()
			return
		}
		// @comment : store it in atomic.Value
		c.curReq.Store(resp)

		if requestBodyRemains(req.Body) {
			registerOnHitEOF(req.Body, resp.conn.reader.startBackgroundRead)
		} else {
			if resp.conn.bufReader.Buffered() > 0 {
				resp.conn.reader.closeNotifyFromPipelinedRequest()
			}
			resp.conn.reader.startBackgroundRead()
		}

		// HTTP cannot have multiple simultaneous active requests.[*]
		// Until the server replies to this request, it can't read another,
		// so we might as well run the handler in this goroutine.
		// [*] Not strictly true: HTTP pipelining.
		// We could let them all process
		// in parallel even if their responses need to be serialized.
		// But we're not going to implement HTTP pipelining because it
		// was never deployed in the wild and the answer is HTTP/2.

		// TODO : @badu - good place for metrics
		// @comment : calls the Handler ServeHTTP(rw ResponseWriter, req *Request)
		serverHandler{srv}.ServeHTTP(resp, resp.req)

		resp.cancelCtx()
		if c.hijacked() {
			return
		}

		// @comment : finishes the request (sending it back to client)
		resp.finishRequest()
		// @comment : certain condition won't let us reuse the connection
		if !resp.shouldReuseConnection() {
			if resp.requestBodyLimitHit || resp.closedRequestBodyEarly() {
				c.closeWriteAndWait()
			}
			return
		}

		srv.setState(c, StateIdle)
		// @comment : store it in atomic.Value
		c.curReq.Store((*response)(nil))

		//if !resp.conn.server.doKeepAlives() {
		if !srv.doKeepAlives() {
			// We're in shutdown mode. We might've replied
			// to the user without "Connection: close" and
			// they might think they can send another
			// request, but such is life with HTTP/1.1.
			return
		}

		// @comment :
		if d := srv.idleTimeout(); d != 0 {
			c.netConIface.SetReadDeadline(time.Now().Add(d))
			if _, err := c.bufReader.Peek(4); err != nil {
				return
			}
		}
		c.netConIface.SetReadDeadline(time.Time{})
	}
}
