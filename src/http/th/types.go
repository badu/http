/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package th

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"flag"
	"net"
	"sync"

	. "http"
	"http/cli"
)

type (
	// A TestServer is an HTTP server listening on a system-chosen port on the
	// local loopback interface, for use in end-to-end HTTP tests.
	TestServer struct {
		mu sync.Mutex // guards closed and conns
		// wg counts the number of outstanding HTTP requests on this server.
		// Close blocks until all requests are finished.
		wg sync.WaitGroup

		URL      string // base URL of form http://ipaddr:port with no trailing slash
		Listener net.Listener

		// TLS is the optional TLS configuration, populated with a new config
		// after TLS is started. If set on an unstarted server before StartTLS
		// is called, existing fields are copied into the new config.
		TLS *tls.Config

		// Config may be changed after calling NewUnstartedServer and
		// before Start or StartTLS.
		Server *Server

		// certificate is a parsed version of the TLS config certificate, if present.
		certificate *x509.Certificate

		conns map[net.Conn]ConnState // except terminal states

		// client is configured for use with the server.
		// Its transport is automatically closed when Close is called.
		client *cli.Client
		closed bool
	}

	// ResponseRecorder is an implementation of http.ResponseWriter that
	// records its mutations for later inspection in tests.
	ResponseRecorder struct {
		// Code is the HTTP response code set by WriteHeader.
		//
		// Note that if a Handler never calls WriteHeader or Write,
		// this might end up being 0, rather than the implicit
		// http.StatusOK. To get the implicit value, use the Result
		// method.
		Code int

		// HeaderMap contains the headers explicitly set by the Handler.
		//
		// To get the implicit headers set by the server (such as
		// automatic Content-Type), use the Result method.
		HeaderMap Header

		// Body is the buffer to which the Handler's Write calls are sent.
		// If nil, the Writes are silently discarded.
		Body *bytes.Buffer

		// Flushed is whether the Handler called Flush.
		Flushed bool

		result      *Response // cache of Result's return value
		snapHeader  Header    // snapshot of HeaderMap at first Write
		wroteHeader bool
	}

	closeIdleTransport interface {
		CloseIdleConnections()
	}
)

var (
	// When debugging a particular http server-based test,
	// this flag lets you run
	//	go test -run=BrokenTest -httptest.serve=127.0.0.1:8000
	// to start the broken server so you can interact with it manually.
	serve = flag.String("httptest.serve", "", "if non-empty, httptest.NewServer serves on this address and blocks")

	// LocalhostCert is a PEM-encoded TLS cert with SAN IPs
	// "127.0.0.1" and "[::1]", expiring at Jan 29 16:00:00 2084 GMT.
	// generated from src/crypto/tls:
	// go run generate_cert.go  --rsa-bits 1024 --host 127.0.0.1,::1,example.com --ca --start-date "Jan 1 00:00:00 1970" --duration=1000000h
	LocalhostCert = []byte(`-----BEGIN CERTIFICATE-----
MIICEzCCAXygAwIBAgIQMIMChMLGrR+QvmQvpwAU6zANBgkqhkiG9w0BAQsFADAS
MRAwDgYDVQQKEwdBY21lIENvMCAXDTcwMDEwMTAwMDAwMFoYDzIwODQwMTI5MTYw
MDAwWjASMRAwDgYDVQQKEwdBY21lIENvMIGfMA0GCSqGSIb3DQEBAQUAA4GNADCB
iQKBgQDuLnQAI3mDgey3VBzWnB2L39JUU4txjeVE6myuDqkM/uGlfjb9SjY1bIw4
iA5sBBZzHi3z0h1YV8QPuxEbi4nW91IJm2gsvvZhIrCHS3l6afab4pZBl2+XsDul
rKBxKKtD1rGxlG4LjncdabFn9gvLZad2bSysqz/qTAUStTvqJQIDAQABo2gwZjAO
BgNVHQ8BAf8EBAMCAqQwEwYDVR0lBAwwCgYIKwYBBQUHAwEwDwYDVR0TAQH/BAUw
AwEB/zAuBgNVHREEJzAlggtleGFtcGxlLmNvbYcEfwAAAYcQAAAAAAAAAAAAAAAA
AAAAATANBgkqhkiG9w0BAQsFAAOBgQCEcetwO59EWk7WiJsG4x8SY+UIAA+flUI9
tyC4lNhbcF2Idq9greZwbYCqTTTr2XiRNSMLCOjKyI7ukPoPjo16ocHj+P3vZGfs
h1fIw3cSS2OolhloGw/XM6RWPWtPAlGykKLciQrBru5NAPvCMsb/I1DAceTiotQM
fblo6RBxUQ==
-----END CERTIFICATE-----`)

	// LocalhostKey is the private key for localhostCert.
	LocalhostKey = []byte(`-----BEGIN RSA PRIVATE KEY-----
MIICXgIBAAKBgQDuLnQAI3mDgey3VBzWnB2L39JUU4txjeVE6myuDqkM/uGlfjb9
SjY1bIw4iA5sBBZzHi3z0h1YV8QPuxEbi4nW91IJm2gsvvZhIrCHS3l6afab4pZB
l2+XsDulrKBxKKtD1rGxlG4LjncdabFn9gvLZad2bSysqz/qTAUStTvqJQIDAQAB
AoGAGRzwwir7XvBOAy5tM/uV6e+Zf6anZzus1s1Y1ClbjbE6HXbnWWF/wbZGOpet
3Zm4vD6MXc7jpTLryzTQIvVdfQbRc6+MUVeLKwZatTXtdZrhu+Jk7hx0nTPy8Jcb
uJqFk541aEw+mMogY/xEcfbWd6IOkp+4xqjlFLBEDytgbIECQQDvH/E6nk+hgN4H
qzzVtxxr397vWrjrIgPbJpQvBsafG7b0dA4AFjwVbFLmQcj2PprIMmPcQrooz8vp
jy4SHEg1AkEA/v13/5M47K9vCxmb8QeD/asydfsgS5TeuNi8DoUBEmiSJwma7FXY
fFUtxuvL7XvjwjN5B30pNEbc6Iuyt7y4MQJBAIt21su4b3sjXNueLKH85Q+phy2U
fQtuUE9txblTu14q3N7gHRZB4ZMhFYyDy8CKrN2cPg/Fvyt0Xlp/DoCzjA0CQQDU
y2ptGsuSmgUtWj3NM9xuwYPm+Z/F84K6+ARYiZ6PYj013sovGKUFfYAqVXVlxtIX
qyUBnu3X9ps8ZfjLZO7BAkEAlT4R5Yl6cGhaJQYZHOde3JEMhNRcVFMO8dJDaFeo
f9Oeos0UUothgiDktdQHxdNEwLjQf7lJJBzV+5OtwswCWA==
-----END RSA PRIVATE KEY-----`)
)
