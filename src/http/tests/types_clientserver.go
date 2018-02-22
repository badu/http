/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package tests

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net"
	"sync"
	"testing"

	. "http"
	"http/cli"
	"http/th"
)

var (
	optQuietLog = func(ts *th.TServer) {
		ts.Config.ErrorLog = log.New(ioutil.Discard, "", 0)
	}
	robotsTxtHandler = HandlerFunc(func(w ResponseWriter, r *Request) {
		w.Header().Set(LastModified, "sometime")
		fmt.Fprintf(w, "User-agent: go\nDisallow: /something/")
	})

	expectedCookies = []*cli.Cookie{
		{Name: "ChocolateChip", Value: "tasty"},
		{Name: "First", Value: "Hit"},
		{Name: "Second", Value: "Hit"},
	}
)

type (
	clientServerTest struct {
		t  *testing.T
		h  Handler
		ts *th.TServer
		tr *Transport
		c  *cli.Client
	}

	reqFunc func(c *cli.Client, url string) (*Response, error)

	// runWrapper is a test helper
	runWrapper struct {
		Handler            func(ResponseWriter, *Request) // required
		ReqFunc            reqFunc                        // optional
		CheckResponse      func(res *Response)            // optional
		EarlyCheckResponse func(res *Response)            // optional; pre-normalize
		Opts               []interface{}
	}

	slurpResult struct {
		io.ReadCloser
		body []byte
		err  error
	}

	lockedBytesBuffer struct {
		sync.Mutex
		bytes.Buffer
	}
	testErrorReader struct{ t *testing.T }

	// Client
	chanWriter         chan string
	recordingTransport struct {
		req *Request
	}
	redirectTest struct {
		suffix       string
		want         int // response code
		redirectBody string
	}

	// Just enough correctness for our redirect tests. Uses the URL.Host as the
	// scope of all cookies.
	TestJar struct {
		m      sync.Mutex
		perURL map[string][]*cli.Cookie
	}

	// RecordingJar keeps a log of calls made to it, without
	// tracking any cookies.
	RecordingJar struct {
		mu  sync.Mutex
		log bytes.Buffer
	}

	writeCountingConn struct {
		net.Conn
		count *int
	}

	// eofReaderFunc is an io.Reader that runs itself, and then returns io.EOF.
	eofReaderFunc func()

	// issue15577Tripper returns a Response with a redirect response
	// header and doesn't populate its Response.Request field.
	issue15577Tripper struct{}

	// issue18239Body is an io.ReadCloser for TestTransportBodyReadError.
	// Its Read returns readErr and increments *readCalls atomically.
	// Its Close returns nil and increments *closeCalls atomically.
	issue18239Body struct {
		readCalls  *int32
		closeCalls *int32
		readErr    error
	}
)
