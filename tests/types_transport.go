/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package tests

import (
	"fmt"
	"io"
	"net"
	"sync"
	"testing"

	. "github.com/badu/http"
	"github.com/badu/http/hdr"
)

var (
	// hostPortHandler writes back the client's "host:port".
	hostPortHandler = HandlerFunc(func(w ResponseWriter, r *Request) {
		if r.FormValue(DoClose) == "true" {
			w.Header().Set(hdr.Connection, DoClose)
		}
		w.Header().Set("X-Saw-Close", fmt.Sprint(r.Close))
		w.Write([]byte(r.RemoteAddr))
	})

	roundTripTests = []struct {
		accept       string
		expectAccept string
		compressed   bool
	}{
		// Requests with no accept-encoding header use transparent compression
		{"", "gzip", false},
		// Requests with other accept-encoding should pass through unmodified
		{"foo", "foo", false},
		// Requests with accept-encoding == gzip should be passed through
		{"gzip", "gzip", true},
	}

	// rgz is a gzip quine that uncompresses to itself.
	rgz = []byte{
		0x1f, 0x8b, 0x08, 0x08, 0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x72, 0x65, 0x63, 0x75, 0x72, 0x73,
		0x69, 0x76, 0x65, 0x00, 0x92, 0xef, 0xe6, 0xe0,
		0x60, 0x00, 0x83, 0xa2, 0xd4, 0xe4, 0xd2, 0xa2,
		0xe2, 0xcc, 0xb2, 0x54, 0x06, 0x00, 0x00, 0x17,
		0x00, 0xe8, 0xff, 0x92, 0xef, 0xe6, 0xe0, 0x60,
		0x00, 0x83, 0xa2, 0xd4, 0xe4, 0xd2, 0xa2, 0xe2,
		0xcc, 0xb2, 0x54, 0x06, 0x00, 0x00, 0x17, 0x00,
		0xe8, 0xff, 0x42, 0x12, 0x46, 0x16, 0x06, 0x00,
		0x05, 0x00, 0xfa, 0xff, 0x42, 0x12, 0x46, 0x16,
		0x06, 0x00, 0x05, 0x00, 0xfa, 0xff, 0x00, 0x05,
		0x00, 0xfa, 0xff, 0x00, 0x14, 0x00, 0xeb, 0xff,
		0x42, 0x12, 0x46, 0x16, 0x06, 0x00, 0x05, 0x00,
		0xfa, 0xff, 0x00, 0x05, 0x00, 0xfa, 0xff, 0x00,
		0x14, 0x00, 0xeb, 0xff, 0x42, 0x88, 0x21, 0xc4,
		0x00, 0x00, 0x14, 0x00, 0xeb, 0xff, 0x42, 0x88,
		0x21, 0xc4, 0x00, 0x00, 0x14, 0x00, 0xeb, 0xff,
		0x42, 0x88, 0x21, 0xc4, 0x00, 0x00, 0x14, 0x00,
		0xeb, 0xff, 0x42, 0x88, 0x21, 0xc4, 0x00, 0x00,
		0x14, 0x00, 0xeb, 0xff, 0x42, 0x88, 0x21, 0xc4,
		0x00, 0x00, 0x00, 0x00, 0xff, 0xff, 0x00, 0x00,
		0x00, 0xff, 0xff, 0x00, 0x17, 0x00, 0xe8, 0xff,
		0x42, 0x88, 0x21, 0xc4, 0x00, 0x00, 0x00, 0x00,
		0xff, 0xff, 0x00, 0x00, 0x00, 0xff, 0xff, 0x00,
		0x17, 0x00, 0xe8, 0xff, 0x42, 0x12, 0x46, 0x16,
		0x06, 0x00, 0x00, 0x00, 0xff, 0xff, 0x01, 0x08,
		0x00, 0xf7, 0xff, 0x3d, 0xb1, 0x20, 0x85, 0xfa,
		0x00, 0x00, 0x00, 0x42, 0x12, 0x46, 0x16, 0x06,
		0x00, 0x00, 0x00, 0xff, 0xff, 0x01, 0x08, 0x00,
		0xf7, 0xff, 0x3d, 0xb1, 0x20, 0x85, 0xfa, 0x00,
		0x00, 0x00, 0x3d, 0xb1, 0x20, 0x85, 0xfa, 0x00,
		0x00, 0x00,
	}
)

type (
	errorReader struct {
		err error
	}

	closerFunc func() error

	writerFuncConn struct {
		net.Conn
		write func(p []byte) (n int, err error)
	}

	fooProto struct{}

	proxyFromEnvTest struct {
		req string // URL to fetch; blank means "http://example.com"

		env      string // HTTP_PROXY
		httpsenv string // HTTPS_PROXY
		noenv    string // NO_PROXY
		reqmeth  string // REQUEST_METHOD

		want    string
		wanterr error
	}

	// byteFromChanReader is an io.Reader that reads a single byte at a
	// time from the channel. When the channel is closed, the reader
	// returns io.EOF.
	byteFromChanReader chan byte

	// testCloseConn is a net.Conn tracked by a testConnSet.
	testCloseConn struct {
		net.Conn
		set *testConnSet
	}

	// testConnSet tracks a set of TCP connections and whether they've
	// been closed.
	testConnSet struct {
		t      *testing.T
		mu     sync.Mutex // guards closed and list
		closed map[net.Conn]bool
		list   []net.Conn // in order created
	}
	countCloseReader struct {
		n *int
		io.Reader
	}
	readerAndCloser struct {
		io.Reader
		io.Closer
	}
	funcConn struct {
		net.Conn
		read  func([]byte) (int, error)
		write func([]byte) (int, error)
	}
	// logWritesConn is a net.Conn that logs each Write call to writes
	// and then proxies to w.
	// It proxies Read calls to a reader it receives from rch.
	logWritesConn struct {
		net.Conn // nil. crash on use.

		w io.Writer

		rch <-chan io.Reader
		r   io.Reader // nil until received by rch

		mu     sync.Mutex
		writes []string
	}
)
