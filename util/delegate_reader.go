/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package util

func (r *delegateReader) Read(p []byte) (int, error) {
	if r.r == nil {
		r.r = <-r.c
	}
	return r.r.Read(p)
}
