/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package chunks

import (
	"bufio"
	"errors"
	"io"
)

const maxLineLength = 4096 // assumed <= bufio.defaultBufSize

var ErrLineTooLong = errors.New("header line too long")

type (
	chunkedReader struct {
		r        *bufio.Reader
		n        uint64 // unread bytes in chunk
		err      error
		buf      [2]byte
		checkEnd bool // whether need to check for \r\n chunk footer
	}

	// Writing to chunkedWriter translates to writing in HTTP chunked Transfer
	// Encoding wire format to the underlying Wire chunkedWriter.
	chunkedWriter struct {
		Wire io.Writer
	}

	// FlushAfterChunkWriter signals from the caller of NewChunkedWriter
	// that each chunk should be followed by a flush. It is used by the
	// http.Transport code to keep the buffering behavior for headers and
	// trailers, but flush out chunks aggressively in the middle for
	// request bodies which may be generated slowly. See Issue 6574.
	FlushAfterChunkWriter struct {
		*bufio.Writer
	}
)
