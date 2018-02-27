/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package tests

import (
	"io"
	"net"
	"sync"
)

var (
	UniqNameMu   sync.Mutex
	UniqNameNext = make(map[string]int)
)

type (
	// loggingConn is used for debugging.
	loggingConn struct {
		name string
		net.Conn
	}

	oneConnListener struct {
		conn net.Conn
	}

	rwTestConn struct {
		io.Reader
		io.Writer
		noopConn

		closeFunc func() error // called if non-nil
		closec    chan bool    // else, if non-nil, send value to it on close
	}
)
