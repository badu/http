/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package sniff

func (m *maskedSig) match(data []byte, firstNonWS int) string {
	// pattern matching algorithm section 6
	// https://mimesniff.spec.whatwg.org/#pattern-matching-algorithm

	if m.skipWS {
		data = data[firstNonWS:]
	}
	if len(m.pat) != len(m.mask) {
		return ""
	}
	if len(data) < len(m.mask) {
		return ""
	}
	for i, mask := range m.mask {
		db := data[i] & mask
		if db != m.pat[i] {
			return ""
		}
	}
	return m.ct
}
