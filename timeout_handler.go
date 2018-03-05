/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package http

import (
	"io"
	"time"

	"github.com/badu/http/hdr"
)

func (h *timeoutHandler) errorBody() string {
	if h.body != "" {
		return h.body
	}
	return "<html><head><title>Timeout</title></head><body><h1>Timeout</h1></body></html>"
}

func (h *timeoutHandler) ServeHTTP(w ResponseWriter, r *Request) {
	var t *time.Timer
	timeout := h.testTimeout
	if timeout == nil {
		t = time.NewTimer(h.dt)
		timeout = t.C
	}
	done := make(chan struct{})
	timeOutWriter := &timeoutWriter{
		respWriter: w,
		header:     make(hdr.Header),
	}
	go func() {
		h.handler.ServeHTTP(timeOutWriter, r)
		close(done)
	}()
	select {
	case <-done:
		timeOutWriter.mu.Lock()
		defer timeOutWriter.mu.Unlock()
		dst := w.Header()
		for k, vv := range timeOutWriter.header {
			dst[k] = vv
		}
		if !timeOutWriter.wroteHeader {
			timeOutWriter.code = StatusOK
		}
		w.WriteHeader(timeOutWriter.code)
		w.Write(timeOutWriter.wbuf.Bytes())
		if t != nil {
			t.Stop()
		}
	case <-timeout:
		timeOutWriter.mu.Lock()
		defer timeOutWriter.mu.Unlock()
		w.WriteHeader(StatusServiceUnavailable)
		io.WriteString(w, h.errorBody())
		timeOutWriter.timedOut = true
		return
	}
}
