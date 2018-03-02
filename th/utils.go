/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package th

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"strconv"
	"strings"

	. "github.com/badu/http"
)

// NewRequest returns a new incoming server Request, suitable
// for passing to an http.Handler for testing.
//
// The target is the RFC 7230 "request-target": it may be either a
// path or an absolute URL. If target is an absolute URL, the host name
// from the URL is used. Otherwise, "example.com" is used.
//
// The TLS field is set to a non-nil dummy value if target has scheme
// "https".
//
// The Request.Proto is always HTTP/1.1.
//
// An empty method means "GET".
//
// The provided body may be nil. If the body is of type *bytes.Reader,
// *strings.Reader, or *bytes.Buffer, the Request.ContentLength is
// set.
//
// NewRequest panics on error for ease of use in testing, where a
// panic is acceptable.
//
// To generate a client HTTP request instead of a server request, see
// the NewRequest function in the net/http package.
func NewTRequest(method, target string, body io.Reader) *Request {
	if method == "" {
		method = GET
	}
	req, err := ReadRequest(bufio.NewReader(strings.NewReader(method + " " + target + " HTTP/1.0\r\n\r\n")))
	if err != nil {
		panic("invalid NewRequest arguments; " + err.Error())
	}

	// HTTP/1.0 was used above to avoid needing a Host field. Change it to 1.1 here.
	req.Proto = HTTP1_1
	req.ProtoMinor = 1
	req.Close = false

	if body != nil {
		switch v := body.(type) {
		case *bytes.Buffer:
			req.ContentLength = int64(v.Len())
		case *bytes.Reader:
			req.ContentLength = int64(v.Len())
		case *strings.Reader:
			req.ContentLength = int64(v.Len())
		default:
			req.ContentLength = -1
		}
		if rc, ok := body.(io.ReadCloser); ok {
			req.Body = rc
		} else {
			req.Body = ioutil.NopCloser(body)
		}
	}

	// 192.0.2.0/24 is "TEST-NET" in RFC 5737 for use solely in
	// documentation and example source code and should not be
	// used publicly.
	req.RemoteAddr = "192.0.2.1:1234"

	if req.Host == "" {
		req.Host = "example.com"
	}

	//@comment : was `if strings.HasPrefix(target, HttpsUrlPrefix) {`
	if len(target) >= len(HttpsUrlPrefix) && target[0:len(HttpsUrlPrefix)] == HttpsUrlPrefix {
		req.TLS = &tls.ConnectionState{
			Version:           tls.VersionTLS12,
			HandshakeComplete: true,
			ServerName:        req.Host,
		}
	}

	return req
}

// NewRecorder returns an initialized ResponseRecorder.
func NewRecorder() *ResponseRecorder {
	return &ResponseRecorder{
		HeaderMap: make(Header),
		Body:      new(bytes.Buffer),
		Code:      200,
	}
}

// parseContentLength trims whitespace from s and returns -1 if no value
// is set, or the value if it's >= 0.
//
// This a modified version of same function found in net/http/transfer.go. This
// one just ignores an invalid header.
func parseContentLength(cl string) int64 {
	cl = strings.TrimSpace(cl)
	if cl == "" {
		return -1
	}
	n, err := strconv.ParseInt(cl, 10, 64)
	if err != nil {
		return -1
	}
	return n
}

func newLocalListener() net.Listener {
	if *serve != "" {
		l, err := net.Listen("tcp", *serve)
		if err != nil {
			panic(fmt.Sprintf("httptest: failed to listen on %v: %v", *serve, err))
		}
		return l
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		if l, err = net.Listen("tcp6", "[::1]:0"); err != nil {
			panic(fmt.Sprintf("httptest: failed to listen on a port: %v", err))
		}
	}
	return l
}

// NewServer starts and returns a new Server.
// The caller should call Close when finished, to shut it down.
func NewServer(handler Handler) *TestServer {
	ts := NewUnstartedServer(handler)
	ts.Start()
	return ts
}

// NewUnstartedServer returns a new Server but doesn't start it.
//
// After changing its configuration, the caller should call Start or
// StartTLS.
//
// The caller should call Close when finished, to shut it down.
func NewUnstartedServer(handler Handler) *TestServer {
	return &TestServer{
		Listener: newLocalListener(),
		Server:   &Server{Handler: handler},
	}
}

// NewTLSServer starts and returns a new Server using TLS.
// The caller should call Close when finished, to shut it down.
func NewTLSServer(handler Handler) *TestServer {
	ts := NewUnstartedServer(handler)
	ts.StartTLS()
	return ts
}
