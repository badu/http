/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package http

func (f finishAsyncByteRead) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	rres := <-f.transferWriter.ByteReadCh
	n, err := rres.n, rres.err
	if n == 1 {
		p[0] = rres.b
	}
	return n, err
}
