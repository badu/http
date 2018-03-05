/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package mime

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"fmt"
	"io"
	"mime"

	. "github.com/badu/http/hdr"
)

// NewWriter returns a new multipart Writer with a random boundary,
// writing to w.
func NewWriter(w io.Writer) *MultipartWriter {
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

func escapeQuotes(s string) string {
	return quoteEscaper.Replace(s)
}

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
func NewReader(r io.Reader, boundary string) *MultipartReader {
	b := []byte("\r\n--" + boundary + "--")
	return &MultipartReader{
		bufReader:        bufio.NewReaderSize(&stickyErrorReader{r: r}, peekBufferSize),
		newLine:          b[:2],
		nlDashBoundary:   b[:len(b)-2],
		dashBoundaryDash: b[2:],
		dashBoundary:     b[2 : len(b)-2],
	}
}

func newPart(mr *MultipartReader) (*SinglePart, error) {
	bp := &SinglePart{
		Header: make(map[string][]string),
		reader: mr,
	}
	if err := bp.populateHeaders(); err != nil {
		return nil, err
	}
	bp.r = partReader{bp}
	if bp.Header.Get(ContentTransferEncoding) == "quoted-printable" {
		bp.Header.Del(ContentTransferEncoding)
		bp.r = NewQuotedReader(bp.r)
	}
	return bp, nil
}

// scanUntilBoundary scans buf to identify how much of it can be safely
// returned as part of the Part body.
// dashBoundary is "--boundary".
// nlDashBoundary is "\r\n--boundary" or "\n--boundary", depending on what mode we are in.
// The comments below (and the name) assume "\n--boundary", but either is accepted.
// total is the number of bytes read out so far. If total == 0, then a leading "--boundary" is recognized.
// readErr is the read error, if any, that followed reading the bytes in buf.
// scanUntilBoundary returns the number of data bytes from buf that can be
// returned as part of the Part body and also the error to return (if any)
// once those data bytes are done.
func scanUntilBoundary(buf, dashBoundary, nlDashBoundary []byte, total int64, readErr error) (int, error) {
	if total == 0 {
		// At beginning of body, allow dashBoundary.
		//@comment : was `if bytes.HasPrefix(buf, dashBoundary) {`
		if len(buf) >= len(dashBoundary) && bytes.Equal(buf[0:len(dashBoundary)], dashBoundary) {
			switch matchAfterPrefix(buf, dashBoundary, readErr) {
			case -1:
				return len(dashBoundary), nil
			case 0:
				return 0, nil
			case +1:
				return 0, io.EOF
			}
		}
		//@comment: was `if bytes.HasPrefix(dashBoundary, buf) {`
		if len(dashBoundary) >= len(buf) && bytes.Equal(dashBoundary[0:len(buf)], buf) {
			return 0, readErr
		}
	}

	// Search for "\n--boundary".
	if i := bytes.Index(buf, nlDashBoundary); i >= 0 {
		switch matchAfterPrefix(buf[i:], nlDashBoundary, readErr) {
		case -1:
			return i + len(nlDashBoundary), nil
		case 0:
			return i, nil
		case +1:
			return i, io.EOF
		}
	}
	//@comment : was `if bytes.HasPrefix(nlDashBoundary, buf) {`
	if len(nlDashBoundary) >= len(buf) && bytes.Equal(nlDashBoundary[0:len(buf)], buf) {
		return 0, readErr
	}

	// Otherwise, anything up to the final \n is not part of the boundary
	// and so must be part of the body.
	// Also if the section from the final \n onward is not a prefix of the boundary,
	// it too must be part of the body.
	i := bytes.LastIndexByte(buf, nlDashBoundary[0])
	//@comment : was `if i >= 0 && bytes.HasPrefix(nlDashBoundary, buf[i:]) {`
	if i >= 0 && len(nlDashBoundary) >= len(buf[i:]) && bytes.Equal(nlDashBoundary[0:len(buf[i:])], buf[i:]) {
		return i, nil
	}
	return len(buf), readErr
}

// matchAfterPrefix checks whether buf should be considered to match the boundary.
// The prefix is "--boundary" or "\r\n--boundary" or "\n--boundary",
// and the caller has verified already that bytes.HasPrefix(buf, prefix) is true.
//
// matchAfterPrefix returns +1 if the buffer does match the boundary,
// meaning the prefix is followed by a dash, space, tab, cr, nl, or end of input.
// It returns -1 if the buffer definitely does NOT match the boundary,
// meaning the prefix is followed by some other character.
// For example, "--foobar" does not match "--foo".
// It returns 0 more input needs to be read to make the decision,
// meaning that len(buf) == len(prefix) and readErr == nil.
func matchAfterPrefix(buf, prefix []byte, readErr error) int {
	if len(buf) == len(prefix) {
		if readErr != nil {
			return +1
		}
		return 0
	}
	c := buf[len(prefix)]
	if c == ' ' || c == '\t' || c == '\r' || c == '\n' || c == '-' {
		return +1
	}
	return -1
}

// skipLWSPChar returns b with leading spaces and tabs removed.
// RFC 822 defines:
//    LWSP-char = SPACE / HTAB
func skipLWSPChar(b []byte) []byte {
	for len(b) > 0 && (b[0] == ' ' || b[0] == '\t') {
		b = b[1:]
	}
	return b
}

// NewReader returns a quoted-printable reader, decoding from r.
func NewQuotedReader(r io.Reader) *QuotedReader {
	return &QuotedReader{
		br: bufio.NewReader(r),
	}
}

func fromHex(b byte) (byte, error) {
	switch {
	case b >= '0' && b <= '9':
		return b - '0', nil
	case b >= 'A' && b <= 'F':
		return b - 'A' + 10, nil
		// Accept badly encoded bytes.
	case b >= 'a' && b <= 'f':
		return b - 'a' + 10, nil
	}
	return 0, fmt.Errorf("quotedprintable: invalid hex byte 0x%02x", b)
}

func readHexByte(v []byte) (b byte, err error) {
	if len(v) < 2 {
		return 0, io.ErrUnexpectedEOF
	}
	var hb, lb byte
	if hb, err = fromHex(v[0]); err != nil {
		return 0, err
	}
	if lb, err = fromHex(v[1]); err != nil {
		return 0, err
	}
	return hb<<4 | lb, nil
}

func isQPDiscardWhitespace(r rune) bool {
	switch r {
	case '\n', '\r', ' ', '\t':
		return true
	}
	return false
}
