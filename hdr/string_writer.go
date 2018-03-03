/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package hdr

func (w stringWriter) WriteString(s string) (int, error) {
	return w.w.Write([]byte(s))
}
