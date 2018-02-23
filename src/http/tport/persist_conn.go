/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package tport

import (
	"fmt"
	"io"
	"log"
	"strings"
	"time"

	. "http"
	"http/trc"
)

// shouldRetryRequest reports whether we should retry sending a failed
// HTTP request on a new connection. The non-nil input error is the
// error from roundTrip.
func (p *persistConn) shouldRetryRequest(req *Request, err error) bool {
	if err == ErrMissingHost {
		// User error.
		return false
	}
	if !p.isReused() {
		// This was a fresh connection. There's no reason the server
		// should've hung up on us.
		//
		// Also, if we retried now, we could loop forever
		// creating new connections and retrying if the server
		// is just hanging up on us because it doesn't like
		// our request (as opposed to sending an error).
		return false
	}
	if _, ok := err.(nothingWrittenError); ok {
		// We never wrote anything, so it's safe to retry, if there's no body or we
		// can "rewind" the body with GetBody.
		return req.OutgoingLength() == 0 || req.GetBody != nil
	}
	if !isReplayable(req) {
		// Don't retry non-idempotent requests.
		return false
	}
	if _, ok := err.(transportReadFromServerError); ok {
		// We got some non-EOF net.Conn.Read failure reading
		// the 1st response byte from the server.
		return true
	}
	if err == ErrServerClosedIdle {
		// The server replied with io.EOF while we were trying to
		// read the response. Probably an unfortunately keep-alive
		// timeout, just as the client was writing a request.
		return true
	}
	return false // conservatively
}

func (p *persistConn) maxHeaderResponseSize() int64 {
	if v := p.transport.MaxResponseHeaderBytes; v != 0 {
		return v
	}
	return 10 << 20 // conservative default; same as http2
}

func (p *persistConn) Read(from []byte) (int, error) {
	if p.readLimit <= 0 {
		return 0, fmt.Errorf("read limit of %d bytes exhausted", p.maxHeaderResponseSize())
	}
	if int64(len(from)) > p.readLimit {
		from = from[:p.readLimit]
	}
	n, err := p.conn.Read(from)
	if err == io.EOF {
		p.sawEOF = true
	}
	p.readLimit -= int64(n)
	return n, err
}

// isBroken reports whether this connection is in a known broken state.
func (p *persistConn) isBroken() bool {
	p.mu.Lock()
	b := p.closed != nil
	p.mu.Unlock()
	return b
}

// canceled returns non-nil if the connection was closed due to
// CancelRequest or due to context cancelation.
func (p *persistConn) canceled() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.canceledErr
}

// isReused reports whether this connection is in a known broken state.
func (p *persistConn) isReused() bool {
	p.mu.Lock()
	r := p.reused
	p.mu.Unlock()
	return r
}

func (p *persistConn) gotIdleConnTrace(idleAt time.Time) trc.GotConnInfo {
	p.mu.Lock()
	defer p.mu.Unlock()
	var t trc.GotConnInfo
	t.Reused = p.reused
	t.Conn = p.conn
	t.WasIdle = true
	if !idleAt.IsZero() {
		t.IdleTime = time.Since(idleAt)
	}
	return t
}

func (p *persistConn) cancelRequest(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.canceledErr = err
	p.closeLocked(ErrRequestCanceled)
}

// closeConnIfStillIdle closes the connection if it's still sitting idle.
// This is what's called by the persistConn's idleTimer, and is run in its
// own goroutine.
func (p *persistConn) closeConnIfStillIdle() {
	t := p.transport
	t.idleMu.Lock()
	defer t.idleMu.Unlock()
	if _, ok := t.idleLRU.m[p]; !ok {
		// Not idle.
		return
	}
	t.removeIdleConnLocked(p)
	p.close(errIdleConnTimeout)
}

// mapRoundTripError returns the appropriate error value for
// persistConn.roundTrip.
//
// The provided err is the first error that (*persistConn).roundTrip
// happened to receive from its select statement.
//
// The startBytesWritten value should be the value of pc.nwrite before the roundTrip
// started writing the request.
func (p *persistConn) mapRoundTripError(req *transportRequest, startBytesWritten int64, err error) error {
	if err == nil {
		return nil
	}

	// If the request was canceled, that's better than network
	// failures that were likely the result of tearing down the
	// connection.
	if cerr := p.canceled(); cerr != nil {
		return cerr
	}

	// See if an error was set explicitly.
	req.mu.Lock()
	reqErr := req.err
	req.mu.Unlock()
	if reqErr != nil {
		return reqErr
	}

	if err == ErrServerClosedIdle {
		// Don't decorate
		return err
	}

	if _, ok := err.(transportReadFromServerError); ok {
		// Don't decorate
		return err
	}
	if p.isBroken() {
		<-p.writeLoopDone
		if p.nwrite == startBytesWritten {
			return nothingWrittenError{err}
		}
		return fmt.Errorf("net/http: HTTP/1.x transport connection broken: %v", err)
	}
	return err
}

func (p *persistConn) readLoop() {
	closeErr := errReadLoopExiting // default value, if not changed below
	defer func() {
		p.close(closeErr)
		p.transport.removeIdleConn(p)
	}()

	tryPutIdleConn := func(trace *trc.ClientTrace) bool {
		if err := p.transport.tryPutIdleConn(p); err != nil {
			closeErr = err
			if trace != nil && trace.PutIdleConn != nil && err != errKeepAlivesDisabled {
				trace.PutIdleConn(err)
			}
			return false
		}
		if trace != nil && trace.PutIdleConn != nil {
			trace.PutIdleConn(nil)
		}
		return true
	}

	// eofc is used to block caller goroutines reading from Response.Body
	// at EOF until this goroutines has (potentially) added the connection
	// back to the idle pool.
	eofc := make(chan struct{})
	defer close(eofc) // unblock reader on errors

	alive := true
	for alive {
		p.readLimit = p.maxHeaderResponseSize()
		_, err := p.br.Peek(1)

		p.mu.Lock()
		if p.numExpectedResponses == 0 {
			p.readLoopPeekFailLocked(err)
			p.mu.Unlock()
			return
		}
		p.mu.Unlock()

		rc := <-p.reqch
		trace := trc.ContextClientTrace(rc.req.Context())

		var resp *Response
		if err == nil {
			resp, err = p.readResponse(rc, trace)
		} else {
			err = transportReadFromServerError{err}
			closeErr = err
		}

		if err != nil {
			if p.readLimit <= 0 {
				err = fmt.Errorf("net/http: server response headers exceeded %d bytes; aborted", p.maxHeaderResponseSize())
			}

			select {
			case rc.ch <- responseAndError{err: err}:
			case <-rc.callerGone:
				return
			}
			return
		}
		p.readLimit = MaxInt64 // effictively no limit for response bodies

		p.mu.Lock()
		p.numExpectedResponses--
		p.mu.Unlock()

		hasBody := rc.req.Method != HEAD && resp.ContentLength != 0

		if resp.Close || rc.req.Close || resp.StatusCode <= 199 {
			// Don't do keep-alive on error if either party requested a close
			// or we get an unexpected informational (1xx) response.
			// StatusCode 100 is already handled above.
			alive = false
		}

		if !hasBody {
			p.transport.setReqCanceler(rc.req, nil)

			// Put the idle conn back into the pool before we send the response
			// so if they process it quickly and make another request, they'll
			// get this same conn. But we use the unbuffered channel 'rc'
			// to guarantee that persistConn.roundTrip got out of its select
			// potentially waiting for this persistConn to close.
			// but after
			alive = alive &&
				!p.sawEOF &&
				p.wroteRequest() &&
				tryPutIdleConn(trace)

			select {
			case rc.ch <- responseAndError{res: resp}:
			case <-rc.callerGone:
				return
			}

			// Now that they've read from the unbuffered channel, they're safely
			// out of the select that also waits on this goroutine to die, so
			// we're allowed to exit now if needed (if alive is false)
			TestEventsEmitter.Dispatch(ReadLoopBeforeNextReadEvent)
			continue
		}

		waitForBodyRead := make(chan bool, 2)
		body := &bodyEOFSignal{
			body: resp.Body,
			earlyCloseFn: func() error {
				waitForBodyRead <- false
				return nil

			},
			fn: func(err error) error {
				isEOF := err == io.EOF
				waitForBodyRead <- isEOF
				if isEOF {
					<-eofc // see comment above eofc declaration
				} else if err != nil {
					if cerr := p.canceled(); cerr != nil {
						return cerr
					}
				}
				return err
			},
		}

		resp.Body = body
		if rc.addedGzip && strings.EqualFold(resp.Header.Get(ContentEncoding), "gzip") {
			resp.Body = &gzipReader{body: body}
			resp.Header.Del(ContentEncoding)
			resp.Header.Del(ContentLength)
			resp.ContentLength = -1
			resp.Uncompressed = true
		}

		select {
		case rc.ch <- responseAndError{res: resp}:
		case <-rc.callerGone:
			return
		}

		// Before looping back to the top of this function and peeking on
		// the bufio.Reader, wait for the caller goroutine to finish
		// reading the response body. (or for cancelation or death)
		select {
		case bodyEOF := <-waitForBodyRead:
			p.transport.setReqCanceler(rc.req, nil) // before p might return to idle pool
			alive = alive &&
				bodyEOF &&
				!p.sawEOF &&
				p.wroteRequest() &&
				tryPutIdleConn(trace)
			if bodyEOF {
				eofc <- struct{}{}
			}
		case <-rc.req.Context().Done():
			alive = false
		case <-p.closech:
			alive = false
		}

		TestEventsEmitter.Dispatch(ReadLoopBeforeNextReadEvent)
	}
}

func (p *persistConn) readLoopPeekFailLocked(peekErr error) {
	if p.closed != nil {
		return
	}
	if n := p.br.Buffered(); n > 0 {
		buf, _ := p.br.Peek(n)
		log.Printf("Unsolicited response received on idle HTTP channel starting with %q; err=%v", buf, peekErr)
	}
	if peekErr == io.EOF {
		// common case.
		p.closeLocked(ErrServerClosedIdle)
	} else {
		p.closeLocked(fmt.Errorf("readLoopPeekFailLocked: %v", peekErr))
	}
}

// readResponse reads an HTTP response (or two, in the case of "Expect:
// 100-continue") from the server. It returns the final non-100 one.
// trace is optional.
func (p *persistConn) readResponse(rc requestAndChan, trace *trc.ClientTrace) (*Response, error) {
	if trace != nil && trace.GotFirstResponseByte != nil {
		if peek, err := p.br.Peek(1); err == nil && len(peek) == 1 {
			trace.GotFirstResponseByte()
		}
	}
	resp, err := ReadResponse(p.br, rc.req)
	if err != nil {
		return resp, err
	}
	if rc.continueCh != nil {
		if resp.StatusCode == 100 {
			if trace != nil && trace.Got100Continue != nil {
				trace.Got100Continue()
			}
			rc.continueCh <- struct{}{}
		} else {
			close(rc.continueCh)
		}
	}
	if resp.StatusCode == 100 {
		p.readLimit = p.maxHeaderResponseSize() // reset the limit
		resp, err = ReadResponse(p.br, rc.req)
		if err != nil {
			return resp, err
		}
	}
	resp.TLS = p.tlsState
	return resp, err
}

// waitForContinue returns the function to block until
// any response, timeout or connection close. After any of them,
// the function returns a bool which indicates if the body should be sent.
func (p *persistConn) waitForContinue(continueCh <-chan struct{}) func() bool {
	if continueCh == nil {
		return nil
	}
	return func() bool {
		timer := time.NewTimer(p.transport.ExpectContinueTimeout)
		defer timer.Stop()

		select {
		case _, ok := <-continueCh:
			return ok
		case <-timer.C:
			return true
		case <-p.closech:
			return false
		}
	}
}

func (p *persistConn) writeLoop() {
	defer close(p.writeLoopDone)
	for {
		select {
		case wr := <-p.writech:
			startBytesWritten := p.nwrite
			err := wr.req.Request.IWrite(p.bw, p.isProxy, wr.req.extra, p.waitForContinue(wr.continueCh))
			if _, ok := err.(RequestBodyReadError); ok {
				//err = bre.error
				// Errors reading from the user's
				// Request.Body are high priority.
				// Set it here before sending on the
				// channels below or calling
				// p.close() which tears town
				// connections and causes other
				// errors.
				wr.req.setError(err)
			}
			if err == nil {
				err = p.bw.Flush()
			}
			if err != nil {
				wr.req.Request.CloseBody()
				if p.nwrite == startBytesWritten {
					err = nothingWrittenError{err}
				}
			}
			p.writeErrCh <- err // to the body reader, which might recycle us
			wr.ch <- err        // to the roundTrip function
			if err != nil {
				p.close(err)
				return
			}
		case <-p.closech:
			return
		}
	}
}

// wroteRequest is a check before recycling a connection that the previous write
// (from writeLoop above) happened and was successful.
func (p *persistConn) wroteRequest() bool {
	select {
	case err := <-p.writeErrCh:
		// Common case: the write happened well before the response, so
		// avoid creating a timer.
		return err == nil
	default:
		// Rare case: the request was written in writeLoop above but
		// before it could send to p.writeErrCh, the reader read it
		// all, processed it, and called us here. In this case, give the
		// write goroutine a bit of time to finish its send.
		//
		// Less rare case: We also get here in the legitimate case of
		// Issue 7569, where the writer is still writing (or stalled),
		// but the server has already replied. In this case, we don't
		// want to wait too long, and we want to return false so this
		// connection isn't re-used.
		select {
		case err := <-p.writeErrCh:
			return err == nil
		case <-time.After(50 * time.Millisecond):
			return false
		}
	}
}

func (p *persistConn) roundTrip(req *transportRequest) (*Response, error) {
	var err error
	//@comment : new way of letting tests know
	TestEventsEmitter.Dispatch(EnterRoundTripEvent)

	if !p.transport.replaceReqCanceler(req.Request, p.cancelRequest) {
		p.transport.putOrCloseIdleConn(p)
		return nil, ErrRequestCanceled
	}
	p.mu.Lock()
	p.numExpectedResponses++
	headerFn := p.mutateHeaderFunc
	p.mu.Unlock()

	if headerFn != nil {
		headerFn(req.extraHeaders())
	}

	// Ask for a compressed version if the caller didn't set their
	// own value for Accept-Encoding. We only attempt to
	// uncompress the gzip stream if we were the layer that
	// requested it.
	requestedGzip := false
	if !p.transport.DisableCompression &&
		req.Header.Get(AcceptEncoding) == "" &&
		req.Header.Get("Range") == "" &&
		req.Method != HEAD {
		// Request gzip only, not deflate. Deflate is ambiguous and
		// not as universally supported anyway.
		// See: http://www.gzip.org/zlib/zlib_faq.html#faq38
		//
		// Note that we don't request this for HEAD requests,
		// due to a bug in nginx:
		//   http://trac.nginx.org/nginx/ticket/358
		//   https://golang.org/issue/5522
		//
		// We don't request gzip if the request is for a range, since
		// auto-decoding a portion of a gzipped document will just fail
		// anyway. See https://golang.org/issue/8923
		requestedGzip = true
		req.extraHeaders().Set(AcceptEncoding, "gzip")
	}

	var continueCh chan struct{}
	if req.ProtoAtLeast(1, 1) && req.Body != nil && req.ExpectsContinue() {
		continueCh = make(chan struct{}, 1)
	}

	if p.transport.DisableKeepAlives {
		req.extraHeaders().Set(Connection, DoClose)
	}

	gone := make(chan struct{})
	defer close(gone)

	defer func() {
		if err != nil {
			p.transport.setReqCanceler(req.Request, nil)
		}
	}()

	const debugRoundTrip = false

	// Write the request concurrently with waiting for a response,
	// in case the server decides to reply before reading our full
	// request body.
	startBytesWritten := p.nwrite
	writeErrCh := make(chan error, 1) //TODO :@badu - this is a very interesting technique - see the var err error above
	p.writech <- writeRequest{req, writeErrCh, continueCh}

	resc := make(chan responseAndError)
	p.reqch <- requestAndChan{
		req:        req.Request,
		ch:         resc,
		addedGzip:  requestedGzip,
		continueCh: continueCh,
		callerGone: gone,
	}

	var respHeaderTimer <-chan time.Time

	ctxDoneChan := req.Context().Done()
	for {
		TestEventsEmitter.Dispatch(WaitResLoopEvent)
		select {
		case err := <-writeErrCh:
			if debugRoundTrip {
				req.logf("writeErrCh resv: %T/%#v", err, err)
			}
			if err != nil {
				p.close(fmt.Errorf("write error: %v", err))
				return nil, p.mapRoundTripError(req, startBytesWritten, err)
			}
			if d := p.transport.ResponseHeaderTimeout; d > 0 {
				if debugRoundTrip {
					req.logf("starting timer for %v", d)
				}
				timer := time.NewTimer(d)
				//TODO : @badu - maybe will be preventing leaks, but it's a defer inside a loop
				defer timer.Stop() // prevent leaks
				respHeaderTimer = timer.C
			}
		case <-p.closech:
			if debugRoundTrip {
				req.logf("closech recv: %T %#v", p.closed, p.closed)
			}
			return nil, p.mapRoundTripError(req, startBytesWritten, p.closed)
		case <-respHeaderTimer:
			if debugRoundTrip {
				req.logf("timeout waiting for response headers.")
			}
			p.close(errTimeout)
			return nil, errTimeout
		case re := <-resc:
			if (re.res == nil) == (re.err == nil) {
				panic(fmt.Sprintf("internal error: exactly one of res or err should be set; nil=%v", re.res == nil))
			}
			if debugRoundTrip {
				req.logf("resc recv: %p, %T/%#v", re.res, re.err, re.err)
			}
			if re.err != nil {
				return nil, p.mapRoundTripError(req, startBytesWritten, re.err)
			}
			return re.res, nil
		case <-ctxDoneChan:
			ctxDoneChan = nil
		}
	}
}

// markReused marks this connection as having been successfully used for a
// request and response.
func (p *persistConn) markReused() {
	p.mu.Lock()
	p.reused = true
	p.mu.Unlock()
}

// close closes the underlying TCP connection and closes
// the pc.closech channel.
//
// The provided err is only for testing and debugging; in normal
// circumstances it should never be seen by users.
func (p *persistConn) close(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closeLocked(err)
}

func (p *persistConn) closeLocked(err error) {
	if err == nil {
		panic("nil error")
	}
	p.broken = true
	if p.closed == nil {
		p.closed = err
		// @comment : HTTP/2 is disabled - we don't need p.alt!=nil
		//if p.alt != nil {
		// Do nothing; can only get here via getConn's
		// handlePendingDial's putOrCloseIdleConn when
		// it turns out the abandoned connection in
		// flight ended up negotiating an alternate
		// protocol. We don't use the connection
		// freelist for http2. That's done by the
		// alternate protocol's RoundTripper.
		//} else {
		p.conn.Close()
		close(p.closech)
		//}
	}
	p.mutateHeaderFunc = nil
}
