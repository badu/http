/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package http

import "github.com/badu/http/hdr"

// RFC 2616: Should treat
//	Pragma: no-cache
// like
//	Cache-Control: no-cache
func fixPragmaCacheControl(header hdr.Header) {
	if hp, ok := header[hdr.Pragma]; ok && len(hp) > 0 && hp[0] == "no-cache" {
		if _, presentcc := header[hdr.CacheControl]; !presentcc {
			header[hdr.CacheControl] = []string{"no-cache"}
		}
	}
}
