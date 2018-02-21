/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package http

func (r errorReader) Read(p []byte) (n int, err error) {
	return 0, r.err
}
