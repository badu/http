/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

// +build race

package tests

import (
	"fmt"
	"os"
)

func init() {
	fmt.Fprint(os.Stderr, "Racing...")
	raceEnabled = true
}
