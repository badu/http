/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package sniff

import "bytes"

func (e *exactSig) match(data []byte, firstNonWS int) string {
	//@comment : was `if bytes.HasPrefix(data, e.sig) {`
	if len(data) >= len(e.sig) && bytes.Equal(data[0:len(e.sig)], e.sig) {
		return e.ct
	}
	return ""
}
