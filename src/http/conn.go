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

	"golang.org/x/net/lex/httplex"
)

func (c *conn) hijacked() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.hijackedv
}

// c.mu must be held.
func (c *conn) hijackLocked() (rwc net.Conn, buf *bufio.ReadWriter, err error) {
	if c.hijackedv {
		return nil, nil, ErrHijacked
	}
	c.r.abortPendingRead()

	c.hijackedv = true
	rwc = c.rwc
	rwc.SetDeadline(time.Time{})

	buf = bufio.NewReadWriter(c.bufr, bufio.NewWriter(rwc))
	if c.r.hasByte {
		if _, err := c.bufr.Peek(c.bufr.Buffered() + 1); err != nil {
			return nil, nil, fmt.Errorf("unexpected Peek failure reading buffered byte: %v", err)
		}
	}
	c.setState(rwc, StateHijacked)
	return
}

// Read next request from connection.
func (c *conn) readRequest(ctx context.Context) (w *response, err error) {
	if c.hijacked() {
		return nil, ErrHijacked
	}

	var (
		wholeReqDeadline time.Time // or zero if none
		hdrDeadline      time.Time // or zero if none
	)

	t0 := time.Now()
	if d := c.server.readHeaderTimeout(); d != 0 {
		hdrDeadline = t0.Add(d)
	}
	if d := c.server.ReadTimeout; d != 0 {
		wholeReqDeadline = t0.Add(d)
	}
	c.rwc.SetReadDeadline(hdrDeadline)
	if d := c.server.WriteTimeout; d != 0 {
		defer func() {
			c.rwc.SetWriteDeadline(time.Now().Add(d))
		}()
	}

	c.r.setReadLimit(c.server.initialReadLimitSize())

	// RFC 2616 section 4.1 tolerance for old buggy clients.
	if c.lastMethod == POST {
		peek, _ := c.bufr.Peek(4) // ReadRequest will get err below
		c.bufr.Discard(numLeadingCRorLF(peek))
	}
	// @comment : reads info from the request (using textproto.Reader transforms bytes into textproto.MIMEHeader and other usefull info)
	req, err := readRequest(c.bufr, false)
	if err != nil {
		if c.r.hitReadLimit() {
			return nil, errTooLarge
		}
		return nil, err
	}

	if !http1ServerSupportsRequest(req) {
		//TODO : @badu - document
		return nil, badRequestError("unsupported protocol version")
	}

	c.lastMethod = req.Method
	c.r.setInfiniteReadLimit()

	hosts, haveHost := req.Header[Host]
	if req.ProtoAtLeast(1, 1) && (!haveHost || len(hosts) == 0) && req.Method != CONNECT {
		//TODO : @badu - document
		return nil, badRequestError("missing required Host header")
	}
	if len(hosts) > 1 {
		//TODO : @badu - document
		return nil, badRequestError("too many Host headers")
	}
	if len(hosts) == 1 && !httplex.ValidHostHeader(hosts[0]) {
		//TODO : @badu - document
		return nil, badRequestError("malformed Host header")
	}
	for k, vv := range req.Header {
		if !httplex.ValidHeaderFieldName(k) {
			//TODO : @badu - document
			return nil, badRequestError("invalid header name")
		}
		for _, v := range vv {
			if !httplex.ValidHeaderFieldValue(v) {
				//TODO : @badu - document
				return nil, badRequestError("invalid header value")
			}
		}
	}
	delete(req.Header, Host)

	ctx, cancelCtx := context.WithCancel(ctx)
	req.ctx = ctx
	req.RemoteAddr = c.remoteAddr
	req.TLS = c.tlsState
	if body, ok := req.Body.(*body); ok {
		body.doEarlyClose = true
	}

	// Adjust the read deadline if necessary.
	if !hdrDeadline.Equal(wholeReqDeadline) {
		c.rwc.SetReadDeadline(wholeReqDeadline)
	}

	w = &response{
		conn:          c,
		cancelCtx:     cancelCtx,
		req:           req,
		reqBody:       req.Body,
		handlerHeader: make(Header),
		contentLength: -1,
		closeNotifyCh: make(chan bool, 1),

		// We populate these ahead of time so we're not
		// reading from req.Header after their Handler starts
		// and maybe mutates it (Issue 14940)
		wants10KeepAlive: req.wantsHttp10KeepAlive(),
		wantsClose:       req.wantsClose(),
	}
	w.cw.res = w
	w.w = newBufioWriterSize(&w.cw, bufferBeforeChunkingSize)
	return w, nil
}

func (c *conn) finalFlush() {
	if c.bufr != nil {
		// Steal the bufio.Reader (~4KB worth of memory) and its associated
		// reader for a future connection.
		putBufioReader(c.bufr)
		c.bufr = nil
	}

	if c.bufw != nil {
		c.bufw.Flush()
		// Steal the bufio.Writer (~4KB worth of memory) and its associated
		// writer for a future connection.
		putBufioWriter(c.bufw)
		c.bufw = nil
	}
}

// Close the connection.
func (c *conn) close() {
	c.finalFlush()
	c.rwc.Close()
}

// closeWrite flushes any outstanding data and sends a FIN packet (if
// client is connected via TCP), signalling that we're done. We then
// pause for a bit, hoping the client processes it before any
// subsequent RST.
//
// See https://golang.org/issue/3595
func (c *conn) closeWriteAndWait() {
	c.finalFlush()
	if tcp, ok := c.rwc.(closeWriter); ok {
		tcp.CloseWrite()
	}
	time.Sleep(rstAvoidanceDelay)
}

func (c *conn) setState(nc net.Conn, state ConnState) {
	srv := c.server
	switch state {
	case StateNew:
		srv.trackConn(c, true)
	case StateHijacked, StateClosed:
		srv.trackConn(c, false)
	}
	c.curState.Store(connStateInterface[state])
	if hook := srv.ConnState; hook != nil {
		hook(nc, state)
	}
}

// Serve a new connection.
//TODO : @badu - maybe this should return error???
func (c *conn) serve(ctx context.Context) {
	c.remoteAddr = c.rwc.RemoteAddr().String()
	ctx = context.WithValue(ctx, LocalAddrContextKey, c.rwc.LocalAddr())

	defer func() {
		// @comment : recovering from panic
		if err := recover(); err != nil && err != ErrAbortHandler {
			const size = 64 << 10
			buf := make([]byte, size)
			buf = buf[:runtime.Stack(buf, false)]
			c.server.logf("http: panic serving %v: %v\n%s", c.remoteAddr, err, buf)
		}
		// @comment :close non hijacked
		if !c.hijacked() {
			c.close()
			c.setState(c.rwc, StateClosed)
		}
	}()

	if tlsConn, ok := c.rwc.(*tls.Conn); ok {
		if d := c.server.ReadTimeout; d != 0 {
			c.rwc.SetReadDeadline(time.Now().Add(d))
		}
		if d := c.server.WriteTimeout; d != 0 {
			c.rwc.SetWriteDeadline(time.Now().Add(d))
		}
		if err := tlsConn.Handshake(); err != nil {
			c.server.logf("http: TLS handshake error from %s: %v", c.rwc.RemoteAddr(), err)
			return
		}
		c.tlsState = new(tls.ConnectionState)
		*c.tlsState = tlsConn.ConnectionState()
		if proto := c.tlsState.NegotiatedProtocol; validNPN(proto) {
			if fn := c.server.TLSNextProto[proto]; fn != nil {
				h := initNPNRequest{tlsConn, serverHandler{c.server}}
				fn(tlsConn, h)
			}
			return
		}
	}

	// HTTP/1.x from here on.
	ctx, cancelCtx := context.WithCancel(ctx)
	c.cancelCtx = cancelCtx
	defer cancelCtx()

	c.r = &connReader{conn: c}
	c.bufr = newBufioReader(c.r)
	c.bufw = newBufioWriterSize(checkConnErrorWriter{c}, 4<<10) //TODO : @badu - this should be configurable

	for {
		// @comment : starts to read request
		resp, err := c.readRequest(ctx)
		if c.r.remain != c.server.initialReadLimitSize() {
			// If we read any bytes off the wire, we're active.
			c.setState(c.rwc, StateActive)
		}
		if err != nil {
			//TODO : @Badu - this should be in consts section
			const errorHeaders = "\r\nContent-Type: text/plain; charset=utf-8\r\nConnection: close\r\n\r\n"

			if err == errTooLarge {
				// Their HTTP client may or may not be
				// able to read this if we're
				// responding to them and hanging up
				// while they're still writing their
				// request. Undefined behavior.
				const publicErr = "431 Request Header Fields Too Large"
				fmt.Fprintf(c.rwc, "HTTP/1.1 "+publicErr+errorHeaders+publicErr)
				c.closeWriteAndWait()
				return
			}
			if isCommonNetReadError(err) {
				return // don't reply
			}

			publicErr := "400 Bad Request"
			if v, ok := err.(badRequestError); ok {
				// //TODO : @badu - document - if conn.readRequest returned an error, it's typed badRequestError
				publicErr = publicErr + ": " + string(v)
			}

			fmt.Fprintf(c.rwc, "HTTP/1.1 "+publicErr+errorHeaders+publicErr)
			return
		}

		// Expect 100 Continue support
		req := resp.req
		if req.expectsContinue() {
			if req.ProtoAtLeast(1, 1) && req.ContentLength != 0 {
				// Wrap the Body reader with one that replies on the connection
				req.Body = &expectContinueReader{readCloser: req.Body, resp: resp}
			}
		} else if req.Header.get(Expect) != "" {
			resp.sendExpectationFailed()
			return
		}
		// @comment : store it in atomic.Value
		c.curReq.Store(resp)

		if requestBodyRemains(req.Body) {
			registerOnHitEOF(req.Body, resp.conn.r.startBackgroundRead)
		} else {
			if resp.conn.bufr.Buffered() > 0 {
				resp.conn.r.closeNotifyFromPipelinedRequest()
			}
			resp.conn.r.startBackgroundRead()
		}

		// HTTP cannot have multiple simultaneous active requests.[*]
		// Until the server replies to this request, it can't read another,
		// so we might as well run the handler in this goroutine.
		// [*] Not strictly true: HTTP pipelining.
		// We could let them all process
		// in parallel even if their responses need to be serialized.
		// But we're not going to implement HTTP pipelining because it
		// was never deployed in the wild and the answer is HTTP/2.

		// @comment : calls the Handler ServeHTTP(rw ResponseWriter, req *Request)
		serverHandler{c.server}.ServeHTTP(resp, resp.req)

		resp.cancelCtx()
		if c.hijacked() {
			return
		}

		// @comment : finishes the request (sending it back to client)
		resp.finishRequest()
		// @comment :
		if !resp.shouldReuseConnection() {
			if resp.requestBodyLimitHit || resp.closedRequestBodyEarly() {
				c.closeWriteAndWait()
			}
			return
		}

		c.setState(c.rwc, StateIdle)
		// @comment : store it in atomic.Value
		c.curReq.Store((*response)(nil))

		if !resp.conn.server.doKeepAlives() {
			// We're in shutdown mode. We might've replied
			// to the user without "Connection: close" and
			// they might think they can send another
			// request, but such is life with HTTP/1.1.
			return
		}

		// @comment : ???
		if d := c.server.idleTimeout(); d != 0 {
			c.rwc.SetReadDeadline(time.Now().Add(d))
			if _, err := c.bufr.Peek(4); err != nil {
				return
			}
		}
		c.rwc.SetReadDeadline(time.Time{})
	}
}
