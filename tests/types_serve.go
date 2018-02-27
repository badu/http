/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package tests

import (
	"bytes"
	"io"
	"io/ioutil"
	"net"
	"sync"
	"testing"
	"time"

	. "github.com/badu/http"
)

type (
	dummyAddr string

	noopConn struct{}

	testConn struct {
		readMu   sync.Mutex // for TestHandlerBodyClose
		readBuf  bytes.Buffer
		writeBuf bytes.Buffer
		closec   chan bool // if non-nil, send value to it on close
		noopConn
	}

	handlerTest struct {
		handler Handler
	}

	// trackLastConnListener tracks the last net.Conn that was accepted.
	trackLastConnListener struct {
		net.Listener

		mu   *sync.RWMutex
		last *net.Conn // destination
	}

	blockingRemoteAddrListener struct {
		net.Listener
		conns chan<- net.Conn
	}

	blockingRemoteAddrConn struct {
		net.Conn
		addrs chan net.Addr
	}

	serverExpectTest struct {
		contentLength    int // of request body
		chunked          bool
		expectation      string // e.g. "100-continue"
		readBody         bool   // whether handler should read the body (if false, sends StatusUnauthorized)
		expectedResponse string // expected substring in first line of http response
	}

	handlerBodyCloseTest struct {
		bodySize     int
		bodyChunked  bool
		reqConnClose bool

		wantEOFSearch bool // should Handler's Body.Close do Reads, looking for EOF?
		wantNextReq   bool // should it find the next request on the same conn?
	}

	// testHandlerBodyConsumer represents a function injected into a test handler to
	// vary work done on a request Body.
	testHandlerBodyConsumer struct {
		name string
		f    func(io.ReadCloser)
	}
	// slowTestConn is a net.Conn that provides a means to simulate parts of a
	// request being received piecemeal. Deadlines can be set and enforced in both
	// Read and Write.
	slowTestConn struct {
		// over multiple calls to Read, time.Durations are slept, strings are read.
		script []interface{}
		closec chan bool

		mu     sync.Mutex // guards rd/wd
		rd, wd time.Time  // read, write deadline
		noopConn
	}

	terrorWriter struct{ t *testing.T }

	neverEnding byte

	countReader struct {
		r io.Reader
		n *int64
	}

	errorListener struct {
		errs []error
	}

	closeWriteTestConn struct {
		rwTestConn
		didCloseWrite bool
	}

	// repeatReader reads content count times, then EOFs.
	repeatReader struct {
		content []byte
		count   int
		off     int
	}
)

var (
	serveMuxRegister = []struct {
		pattern string
		h       Handler
	}{
		{"/dir/", serve(200)},
		{"/search", serve(201)},
		{"codesearch.google.com/search", serve(202)},
		{"codesearch.google.com/", serve(203)},
		{"example.com/", HandlerFunc(checkQueryStringHandler)},
	}

	serveMuxTests = []struct {
		method  string
		host    string
		path    string
		code    int
		pattern string
	}{
		{GET, "google.com", "/", 404, ""},
		{GET, "google.com", "/dir", 301, "/dir/"},
		{GET, "google.com", "/dir/", 200, "/dir/"},
		{GET, "google.com", "/dir/file", 200, "/dir/"},
		{GET, "google.com", "/search", 201, "/search"},
		{GET, "google.com", "/search/", 404, ""},
		{GET, "google.com", "/search/foo", 404, ""},
		{GET, "codesearch.google.com", "/search", 202, "codesearch.google.com/search"},
		{GET, "codesearch.google.com", "/search/", 203, "codesearch.google.com/"},
		{GET, "codesearch.google.com", "/search/foo", 203, "codesearch.google.com/"},
		{GET, "codesearch.google.com", "/", 203, "codesearch.google.com/"},
		{GET, "codesearch.google.com:443", "/", 203, "codesearch.google.com/"},
		{GET, "images.google.com", "/search", 201, "/search"},
		{GET, "images.google.com", "/search/", 404, ""},
		{GET, "images.google.com", "/search/foo", 404, ""},
		{GET, "google.com", "/../search", 301, "/search"},
		{GET, "google.com", "/dir/..", 301, ""},
		{GET, "google.com", "/dir/..", 301, ""},
		{GET, "google.com", "/dir/./file", 301, "/dir/"},

		// The /foo -> /foo/ redirect applies to CONNECT requests
		// but the path canonicalization does not.
		{CONNECT, "google.com", "/dir", 301, "/dir/"},
		{CONNECT, "google.com", "/../search", 404, ""},
		{CONNECT, "google.com", "/dir/..", 200, "/dir/"},
		{CONNECT, "google.com", "/dir/..", 200, "/dir/"},
		{CONNECT, "google.com", "/dir/./file", 200, "/dir/"},
	}

	serveMuxTests2 = []struct {
		method  string
		host    string
		url     string
		code    int
		redirOk bool
	}{
		{GET, "google.com", "/", 404, false},
		{GET, "example.com", "/test/?example.com/test/", 200, false},
		{GET, "example.com", "test/?example.com/test/", 200, true},
	}

	serverExpectTests = []serverExpectTest{
		// Normal 100-continues, case-insensitive.
		expectTest(100, "100-continue", true, "100 Continue"),
		expectTest(100, "100-cOntInUE", true, "100 Continue"),

		// No 100-continue.
		expectTest(100, "", true, "200 OK"),

		// 100-continue but requesting client to deny us,
		// so it never reads the body.
		expectTest(100, "100-continue", false, "401 Unauthorized"),
		// Likewise without 100-continue:
		expectTest(100, "", false, "401 Unauthorized"),

		// Non-standard expectations are failures
		expectTest(0, "a-pony", false, "417 Expectation Failed"),

		// Expect-100 requested but no body (is apparently okay: Issue 7625)
		expectTest(0, "100-continue", true, "200 OK"),
		// Expect-100 requested but handler doesn't read the body
		expectTest(0, "100-continue", false, "401 Unauthorized"),
		// Expect-100 continue with no body, but a chunked body.
		{
			expectation:      "100-continue",
			readBody:         true,
			chunked:          true,
			expectedResponse: "100 Continue",
		},
	}

	handlerBodyCloseTests = [...]handlerBodyCloseTest{
		// Small enough to slurp past to the next request +
		// has Content-Length.
		0: {
			bodySize:      20 << 10,
			bodyChunked:   false,
			reqConnClose:  false,
			wantEOFSearch: true,
			wantNextReq:   true,
		},

		// Small enough to slurp past to the next request +
		// is chunked.
		1: {
			bodySize:      20 << 10,
			bodyChunked:   true,
			reqConnClose:  false,
			wantEOFSearch: true,
			wantNextReq:   true,
		},

		// Small enough to slurp past to the next request +
		// has Content-Length +
		// declares Connection: close (so pointless to read more).
		2: {
			bodySize:      20 << 10,
			bodyChunked:   false,
			reqConnClose:  true,
			wantEOFSearch: false,
			wantNextReq:   false,
		},

		// Small enough to slurp past to the next request +
		// declares Connection: close,
		// but chunked, so it might have trailers.
		// TODO: maybe skip this search if no trailers were declared
		// in the headers.
		3: {
			bodySize:      20 << 10,
			bodyChunked:   true,
			reqConnClose:  true,
			wantEOFSearch: true,
			wantNextReq:   false,
		},

		// Big with Content-Length, so give up immediately if we know it's too big.
		4: {
			bodySize:      1 << 20,
			bodyChunked:   false, // has a Content-Length
			reqConnClose:  false,
			wantEOFSearch: false,
			wantNextReq:   false,
		},

		// Big chunked, so read a bit before giving up.
		5: {
			bodySize:      1 << 20,
			bodyChunked:   true,
			reqConnClose:  false,
			wantEOFSearch: true,
			wantNextReq:   false,
		},

		// Big with Connection: close, but chunked, so search for trailers.
		// TODO: maybe skip this search if no trailers were declared
		// in the headers.
		6: {
			bodySize:      1 << 20,
			bodyChunked:   true,
			reqConnClose:  true,
			wantEOFSearch: true,
			wantNextReq:   false,
		},

		// Big with Connection: close, so don't do any reads on Close.
		// With Content-Length.
		7: {
			bodySize:      1 << 20,
			bodyChunked:   false,
			reqConnClose:  true,
			wantEOFSearch: false,
			wantNextReq:   false,
		},
	}

	testHandlerBodyConsumers = []testHandlerBodyConsumer{
		{"nil", func(io.ReadCloser) {}},
		{DoClose, func(r io.ReadCloser) { r.Close() }},
		{"discard", func(r io.ReadCloser) { io.Copy(ioutil.Discard, r) }},
	}

	// A Response that's just no bigger than 2KB, the buffer-before-chunking threshold.
	response = bytes.Repeat([]byte(someResponse), 2<<10/len(someResponse))
)

const someResponse = "<html>some response</html>"
