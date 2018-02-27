// +build !windows

/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package tests

import (
	"runtime"
)

func hasSymlink() (ok bool, reason string) {
	switch runtime.GOOS {
	case "android", "nacl", "plan9":
		return false, ""
	}

	return true, ""
}
