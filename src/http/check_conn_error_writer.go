/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package http

func (w checkConnErrorWriter) Write(p []byte) (n int, err error) {
	n, err = w.c.netConIface.Write(p)
	if err != nil && w.c.wErr == nil {
		w.c.wErr = err
		w.c.cancelCtx()
	}
	return
}
