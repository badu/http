/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package sniff

func (textSig) match(data []byte, firstNonWS int) string {
	// c.f. section 5, step 4.
	for _, b := range data[firstNonWS:] {
		switch {
		case b <= 0x08,
			b == 0x0B,
			0x0E <= b && b <= 0x1A,
			0x1C <= b && b <= 0x1F:
			return ""
		}
	}
	return "text/plain; charset=utf-8"
}
