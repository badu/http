/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package http

import (
	"net"
	"time"
)

func (l tcpKeepAliveListener) Accept() (net.Conn, error) {
	conn, err := l.AcceptTCP() // accepts the next incoming call and returns the new connection.
	if err != nil {
		return conn, err
	}
	conn.SetKeepAlive(true)
	conn.SetKeepAlivePeriod(3 * time.Minute) // TODO : @badu - this should be configurable
	return conn, nil
}
