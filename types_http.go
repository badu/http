/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package http

import (
	"io"
	"sync"
	"time"
)

const (
	// MaxInt64 is the effective "infinite" value for the Server and
	// Transport's byte-limiting readers.
	MaxInt64 = 1<<63 - 1

	// The algorithm uses at most sniffLen bytes to make its decision.
	SniffLen = 512
)

var (
	// aLongTimeAgo is a non-zero time, far in the past, used for
	// immediate cancelation of network operations.
	aLongTimeAgo = time.Unix(1, 0)

	// NoBody is an io.ReadCloser with no bytes. Read always returns EOF
	// and Close always returns nil. It can be used in an outgoing client
	// request to explicitly signal that a request has zero bytes.
	// An alternative, however, is to simply set Request.Body to nil.
	NoBody = noBody{}
	// verify that an io.Copy from NoBody won't require a buffer:
	_ io.WriterTo   = NoBody
	_ io.ReadCloser = NoBody
)

type (
	// contextKey is a value for use with context.WithValue. It's used as
	// a pointer so it fits in an interface{} without allocation.
	contextKey struct {
		name string
	}
	noBody struct{}

	// PushOptions describes options for Pusher.Push.
	PushOptions struct {
		// Method specifies the HTTP method for the promised request.
		// If set, it must be "GET" or "HEAD". Empty means "GET".
		Method string

		// Header specifies additional promised request headers. This cannot
		// include HTTP/2 pseudo header fields like ":path" and ":scheme",
		// which will be added automatically.
		Header Header
	}

	// Pusher is the interface implemented by ResponseWriters that support
	// HTTP/2 server push. For more background, see
	// https://tools.ietf.org/html/rfc7540#section-8.2.
	Pusher interface {
		// Push initiates an HTTP/2 server push. This constructs a synthetic
		// request using the given target and options, serializes that request
		// into a PUSH_PROMISE frame, then dispatches that request using the
		// server's request handler. If opts is nil, default options are used.
		//
		// The target must either be an absolute path (like "/path") or an absolute
		// URL that contains a valid host and the same scheme as the parent request.
		// If the target is a path, it will inherit the scheme and host of the
		// parent request.
		//
		// The HTTP/2 spec disallows recursive pushes and cross-authority pushes.
		// Push may or may not detect these invalid pushes; however, invalid
		// pushes will be detected and canceled by conforming clients.
		//
		// Handlers that wish to push URL X should call Push before sending any
		// data that may trigger a request for URL X. This avoids a race where the
		// client issues requests for X before receiving the PUSH_PROMISE for X.
		//
		// Push returns ErrNotSupported if the client has disabled push or if push
		// is not supported on the underlying connection.
		Push(target string, opts *PushOptions) error
	}
	/**
	srvEvDispatcher : added to get rid of the dependencies on fakeLocker and all the test hooks
	*/
	ServerEventType int
	srvEvDispatcher struct {
		lsns map[ServerEventType][]srvEvListner
		mu   sync.RWMutex
	}

	srvEvListner struct {
		ch chan ServerEventType
	}

	ServerEventHandler struct {
		sync.WaitGroup
		ch          chan ServerEventType // channel for receiving events
		handler     func()               // function which gets called if event is met
		eventType   ServerEventType      // which kind of event we're listening to
		willRemount bool                 // internal, so we can continuosly listen
	}
)

const (
	killListeners               ServerEventType = 0
	ServerServe                 ServerEventType = 1
	EnterRoundTripEvent         ServerEventType = 2
	RoundTripRetriedEvent       ServerEventType = 3
	PrePendingDialEvent         ServerEventType = 4
	PostPendingDialEvent        ServerEventType = 5
	WaitResLoopEvent            ServerEventType = 6
	ReadLoopBeforeNextReadEvent ServerEventType = 7
)

var (
	TestEventsEmitter = &srvEvDispatcher{lsns: map[ServerEventType][]srvEvListner{}}
)
