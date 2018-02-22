/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package mime

import (
	"io"
	"strings"
)

// A Writer generates multipart messages.
type Writer struct {
	w        io.Writer
	boundary string
	lastpart *part
}

var quoteEscaper = strings.NewReplacer("\\", "\\\\", `"`, "\\\"")

type part struct {
	mw     *Writer
	closed bool
	we     error // last error that occurred writing
}
