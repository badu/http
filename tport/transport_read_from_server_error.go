/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package tport

import "fmt"

func (e transportReadFromServerError) Error() string {
	return fmt.Sprintf("github.com/badu/http/tport: Transport failed to read from server: %v", e.err)
}
