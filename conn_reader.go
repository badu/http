/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package http

import (
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

func (c *connReader) lock() {
	c.mu.Lock()
	if c.cond == nil {
		// @comment : make a new condition having the locker c.mu
		c.cond = sync.NewCond(&c.mu)
	}
}

func (c *connReader) unlock() { c.mu.Unlock() }

// @comment : called directly and also callback which is set to body{}
func (c *connReader) startBackgroundRead() {
	c.lock()
	defer c.unlock()
	if c.inRead {
		panic("invalid concurrent Body.Read call")
	}
	if c.hasByte {
		return
	}
	c.inRead = true
	c.conn.netConIface.SetReadDeadline(time.Time{})
	go c.backgroundRead()
}

func (c *connReader) backgroundRead() {
	n, err := c.conn.netConIface.Read(c.byteBuf[:])
	c.lock()
	if n == 1 {
		c.hasByte = true
		// We were at EOF already (since we wouldn't be in a
		// background read otherwise), so this is a pipelined
		// HTTP request.
		c.closeNotifyFromPipelinedRequest()
	}
	// @comment : net.Error is an interface : Timeout() bool and Temporary() bool
	if ne, ok := err.(net.Error); ok && c.aborted && ne.Timeout() {
		// Ignore this error. It's the expected error from
		// another goroutine calling abortPendingRead.
	} else if err != nil {
		c.handleReadError(err)
	}
	c.aborted = false
	c.inRead = false
	c.unlock()
	//@comment : wake all goroutines waiting on condition.
	c.cond.Broadcast()
}

func (c *connReader) abortPendingRead() {
	c.lock()
	defer c.unlock()
	if !c.inRead {
		return
	}
	c.aborted = true
	c.conn.netConIface.SetReadDeadline(aLongTimeAgo)
	//@comment : wait awoken by Broadcast or Signal
	for c.inRead {
		c.cond.Wait()
	}
	c.conn.netConIface.SetReadDeadline(time.Time{})
}

func (c *connReader) setReadLimit(remain int64) { c.remain = remain }

func (c *connReader) setInfiniteReadLimit() { c.remain = MaxInt64 }

func (c *connReader) hitReadLimit() bool { return c.remain <= 0 }

// may be called from multiple goroutines.
func (c *connReader) handleReadError(err error) {
	c.conn.cancelCtx()
	c.closeNotify()
}

// closeNotifyFromPipelinedRequest simply calls closeNotify.
//
// This method wrapper is here for documentation. The callers are the
// cases where we send on the closenotify channel because of a
// pipelined HTTP request, per the previous Go behavior and
// documentation (that this "MAY" happen).
//
// TODO: consider changing this behavior and making context cancelation and closenotify work the same.
func (c *connReader) closeNotifyFromPipelinedRequest() {
	c.closeNotify()
}

// may be called from multiple goroutines.
func (c *connReader) closeNotify() {
	// @comment : loads it from atomic value
	res, _ := c.conn.curReq.Load().(*response)
	if res != nil {
		if atomic.CompareAndSwapInt32(&res.didCloseNotify, 0, 1) {
			res.closeNotifyCh <- true
		}
	}
}

func (c *connReader) Read(p []byte) (int, error) {
	c.lock()
	if c.inRead {
		c.unlock()
		panic("invalid concurrent Body.Read call")
	}
	if c.hitReadLimit() {
		c.unlock()
		return 0, io.EOF
	}
	if len(p) == 0 {
		c.unlock()
		return 0, nil
	}
	if int64(len(p)) > c.remain {
		p = p[:c.remain]
	}
	if c.hasByte {
		p[0] = c.byteBuf[0]
		c.hasByte = false
		c.unlock()
		return 1, nil
	}
	c.inRead = true
	c.unlock()
	n, err := c.conn.netConIface.Read(p)

	c.lock()
	c.inRead = false
	if err != nil {
		c.handleReadError(err)
	}
	c.remain -= int64(n)
	c.unlock()
	//@comment : wake all goroutines waiting on condition.
	c.cond.Broadcast()
	return n, err
}
