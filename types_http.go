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
