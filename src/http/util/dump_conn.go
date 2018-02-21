/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package util

import (
	"net"
	"time"
)

func (c *dumpConn) Close() error { return nil }

func (c *dumpConn) LocalAddr() net.Addr { return nil }

func (c *dumpConn) RemoteAddr() net.Addr { return nil }

func (c *dumpConn) SetDeadline(t time.Time) error { return nil }

func (c *dumpConn) SetReadDeadline(t time.Time) error { return nil }

func (c *dumpConn) SetWriteDeadline(t time.Time) error { return nil }
