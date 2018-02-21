/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package http

import (
	"io"
	"net"
)

func (d oneConnDialer) Dial(network, addr string) (net.Conn, error) {
	select {
	case c := <-d:
		return c, nil
	default:
		return nil, io.EOF
	}
}
