/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package http

import "io"

func (br transferBodyReader) Read(p []byte) (n int, err error) {
	n, err = br.tw.Body.Read(p)
	if err != nil && err != io.EOF {
		br.tw.bodyReadError = err
	}
	return
}
