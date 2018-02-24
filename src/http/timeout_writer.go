/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package http

func (t *timeoutWriter) Header() Header { return t.h }

func (t *timeoutWriter) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.timedOut {
		return 0, ErrHandlerTimeout
	}
	if !t.wroteHeader {
		t.writeHeader(StatusOK)
	}
	return t.wbuf.Write(p)
}

func (t *timeoutWriter) WriteHeader(code int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.timedOut || t.wroteHeader {
		return
	}
	t.writeHeader(code)
}

func (t *timeoutWriter) writeHeader(code int) {
	t.wroteHeader = true
	t.code = code
}
