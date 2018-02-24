/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package http

import "io"

func (b transferBodyReader) Read(p []byte) (int, error) {
	n, err := b.transferWriter.Body.Read(p)
	if err != nil && err != io.EOF {
		b.transferWriter.bodyReadError = err
	}
	return n, err
}
