/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package util

import (
	"errors"
	"io"
	"io/ioutil"
	"log"
	"strings"
	"sync"
	"time"

	. "github.com/badu/http"
	. "github.com/badu/http/tport"
)

// This is an API usage error - the local side is closed.
// ErrPersistEOF (above) reports that the remote side is closed.
//var errClosed = errors.New("i/o operation on closed connection")

type (
	// dumpConn is a net.Conn which writes to Writer and reads from Reader
	dumpConn struct {
		io.Writer
		io.Reader
	}

	neverEnding byte

	// delegateReader is a reader that delegates to another reader,
	// once it arrives on a channel.
	delegateReader struct {
		c chan io.Reader
		r io.Reader // nil until received from c
	}
	// failureToReadBody is a io.ReadCloser that just returns errNoBody on
	// Read. It's swapped in when we don't actually want to consume
	// the body, but need a non-nil one, and want to distinguish the
	// error from reading the dummy body.
	failureToReadBody struct{}

	// ReverseProxy is an HTTP Handler that takes an incoming request and
	// sends it to another server, proxying the response back to the
	// client.
	ReverseProxy struct {
		// Director must be a function which modifies
		// the request into a new request to be sent
		// using Transport. Its response is then copied
		// back to the original client unmodified.
		// Director must not access the provided Request
		// after returning.
		Director func(*Request)

		// The transport used to perform proxy requests.
		// If nil, http.DefaultTransport is used.
		Transport RoundTripper

		// FlushInterval specifies the flush interval
		// to flush to the client while copying the
		// response body.
		// If zero, no periodic flushing is done.
		FlushInterval time.Duration

		// ErrorLog specifies an optional logger for errors
		// that occur when attempting to proxy the request.
		// If nil, logging goes to os.Stderr via the log package's
		// standard logger.
		ErrorLog *log.Logger

		// BufferPool optionally specifies a buffer pool to
		// get byte slices for use by io.CopyBuffer when
		// copying HTTP response bodies.
		BufferPool BufferPool

		// ModifyResponse is an optional function that
		// modifies the Response from the backend.
		// If it returns an error, the proxy returns a StatusBadGateway error.
		ModifyResponse func(*Response) error
	}

	// A BufferPool is an interface for getting and returning temporary
	// byte slices for use by io.CopyBuffer.
	// TODO : @badu - check it out. Why use of interface?
	BufferPool interface {
		Get() []byte
		Put([]byte)
	}
	// TODO : @badu - seen in tests?
	writeFlusher interface {
		io.Writer
		Flusher
	}

	maxLatencyWriter struct {
		dst     writeFlusher
		latency time.Duration

		mu   sync.Mutex // protects Write + Flush
		done chan bool
	}
)

var (
	// TODO : @badu - seems these are present somewhere else
	reqWriteExcludeHeaderDump = map[string]bool{
		Host:             true, // not in Header map anyway
		TransferEncoding: true,
		Trailer:          true,
	}

	// errNoBody is a sentinel error value used by failureToReadBody so we
	// can detect that the lack of body was intentional.
	errNoBody = errors.New("sentinel error value")

	// emptyBody is an instance of empty reader.
	emptyBody = ioutil.NopCloser(strings.NewReader(""))

	// onExitFlushLoop is a callback set by tests to detect the state of the
	// flushLoop() goroutine.
	onExitFlushLoop func()
	// TODO : @badu - seems these are present somewhere else
	// Hop-by-hop headers. These are removed when sent to the backend.
	// http://www.w3.org/Protocols/rfc2616/rfc2616-sec13.html
	hopHeaders = []string{
		Connection,
		ProxyConnection, // non-standard but still sent by libcurl and rejected by e.g. google
		KeepAlive,
		ProxyAuthenticate,
		ProxyAuthorization,
		Te,      // canonicalized version of "TE"
		Trailer, // not Trailers per URL above; http://www.rfc-editor.org/errata_search.php?eid=4522
		TransferEncoding,
		UpgradeHeader,
	}
	doubleCRLF = []byte("\r\n\r\n")
)
