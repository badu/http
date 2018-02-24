/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package http

import "io"

func (r *expectContinueReader) Read(p []byte) (int, error) {
	if r.closed {
		return 0, ErrBodyReadAfterClose
	}
	if !r.resp.wroteContinue && !r.resp.conn.hijacked() {
		r.resp.wroteContinue = true
		r.resp.conn.bufWriter.WriteString("HTTP/1.1 100 Continue\r\n\r\n")
		r.resp.conn.bufWriter.Flush()
	}
	n, err := r.readCloser.Read(p)
	if err == io.EOF {
		r.sawEOF = true
	}
	return n, err
}

func (r *expectContinueReader) Close() error {
	r.closed = true
	return r.readCloser.Close()
}
