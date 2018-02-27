/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package tport

import (
	. "http"
	"time"
)

func (t *transportRequest) extraHeaders() Header {
	if t.extra == nil {
		t.extra = make(Header)
	}
	return t.extra
}

func (t *transportRequest) setError(err error) {
	t.mu.Lock()
	if t.err == nil {
		t.err = err
	}
	t.mu.Unlock()
}

func (t *transportRequest) logf(format string, args ...interface{}) {
	if logf, ok := t.Request.Context().Value(TLogKey{}).(func(string, ...interface{})); ok {
		logf(time.Now().Format(time.RFC3339Nano)+": "+format, args...)
	}
}
