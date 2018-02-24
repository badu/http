/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package http

import "io"

func (b transferBodyReader) Read(p []byte) (int, error) {
	n, err := b.transferWriter.Body.Read(p)
	if err != nil && err != io.EOF {
		// TODO : @badu - read the comment below
		//@comment : I hate this anti-pattern - an error is being passed to a property of a property
		b.transferWriter.bodyReadError = err
	}
	return n, err
}
