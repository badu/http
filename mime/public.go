/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package mime

import (
	"bufio"
	"crypto/rand"
	"fmt"
	"io"
	"mime"
)

func MIMETypeByExtension(ext string) string {
	return mime.TypeByExtension(ext)
}

func MIMEParseMediaType(v string) (string, map[string]string, error) {
	return mime.ParseMediaType(v)
}

// NewReader creates a new multipart Reader reading from r using the
// given MIME boundary.
//
// The boundary is usually obtained from the "boundary" parameter of
// the message's "Content-Type" header. Use ParseMediaType to
// parse such headers.
func NewMultipartReader(r io.Reader, boundary string) *MultipartReader {
	b := []byte("\r\n--" + boundary + "--")
	return &MultipartReader{
		bufReader:        bufio.NewReaderSize(&stickyErrorReader{r: r}, peekBufferSize),
		newLine:          b[:2],
		nlDashBoundary:   b[:len(b)-2],
		dashBoundaryDash: b[2:],
		dashBoundary:     b[2 : len(b)-2],
	}
}

// NewWriter returns a new multipart Writer with a random boundary,
// writing to w.
func NewMultipartWriter(w io.Writer) *MultipartWriter {
	var buf [30]byte
	_, err := io.ReadFull(rand.Reader, buf[:])
	if err != nil {
		panic(err)
	}
	return &MultipartWriter{
		w:        w,
		boundary: fmt.Sprintf("%x", buf[:]),
	}
}

// NewReader returns a quoted-printable reader, decoding from r.
func NewQuotedReader(r io.Reader) *QuotedReader {
	return &QuotedReader{
		br: bufio.NewReader(r),
	}
}
