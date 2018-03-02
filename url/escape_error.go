/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package url

import "strconv"

func (e EscapeError) Error() string {
	return "invalid URL escape " + strconv.Quote(string(e))
}
