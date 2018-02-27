/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package mime

import (
	"crypto/rand"
	"fmt"
	"io"
	"mime"
)

// NewWriter returns a new multipart Writer with a random boundary,
// writing to w.
func NewWriter(w io.Writer) *Writer {
	return &Writer{
		w:        w,
		boundary: randomBoundary(),
	}
}

func randomBoundary() string {
	var buf [30]byte
	_, err := io.ReadFull(rand.Reader, buf[:])
	if err != nil {
		panic(err)
	}
	return fmt.Sprintf("%x", buf[:])
}

func escapeQuotes(s string) string {
	return quoteEscaper.Replace(s)
}

func TypeByExtension(ext string) string {
	return mime.TypeByExtension(ext)
}
