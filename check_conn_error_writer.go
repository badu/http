/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package http

func (w checkConnErrorWriter) Write(p []byte) (int, error) {
	n, err := w.con.netConIface.Write(p)
	if err != nil && w.con.wErr == nil {
		w.con.wErr = err
		w.con.cancelCtx()
	}
	return n, err
}
