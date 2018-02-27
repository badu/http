/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package tport

func (e *httpError) Error() string { return e.err }

func (e *httpError) Timeout() bool { return e.timeout }

func (e *httpError) Temporary() bool { return true }
