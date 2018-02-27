/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package http

func (b bodyLocked) Read(p []byte) (int, error) {
	if b.body.isClosed {
		return 0, ErrBodyReadAfterClose
	}
	return b.body.readLocked(p)
}
