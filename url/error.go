/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package url

func (e *Error) Error() string { return e.Op + " " + e.URL + ": " + e.Err.Error() }

func (e *Error) Timeout() bool {
	t, ok := e.Err.(timeout)
	return ok && t.Timeout()
}

func (e *Error) Temporary() bool {
	t, ok := e.Err.(temporary)
	return ok && t.Temporary()
}
