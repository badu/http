/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package hdr

import (
	"bufio"
	"time"
)

// ParseTime parses a time header (such as the Date: header),
// trying each of the three formats allowed by HTTP/1.1:
// TimeFormat, time.RFC850, and time.ANSIC.
func ParseTime(text string) (time.Time, error) {
	var t time.Time
	var err error
	for _, layout := range timeFormats {
		t, err = time.Parse(layout, text)
		if err == nil {
			return t, err
		}
	}
	return t, err
}

// TrimString returns s without leading and trailing ASCII space.
func TrimString(s string) string {
	for len(s) > 0 && isASCIISpace(s[0]) {
		s = s[1:]
	}
	for len(s) > 0 && isASCIISpace(s[len(s)-1]) {
		s = s[:len(s)-1]
	}
	return s
}

// NewHeaderReader returns a new Reader reading from r.
//
// To avoid denial of service attacks, the provided bufio.Reader
// should be reading from an io.LimitReader or similar Reader to bound
// the size of responses.
func NewHeaderReader(r *bufio.Reader) *HeaderReader {
	return &HeaderReader{R: r}
}

// CanonicalHeaderKey returns the canonical format of the
// MIME header key s. The canonicalization converts the first
// letter and any letter following a hyphen to upper case;
// the rest are converted to lowercase. For example, the
// canonical key for "accept-encoding" is "Accept-Encoding".
// MIME header keys are assumed to be ASCII only.
// If s contains a space or invalid header field bytes, it is
// returned without modifications.
func CanonicalHeaderKey(s string) string {
	// Quick check for canonical encoding.
	upper := true
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !validHeaderFieldByte(c) {
			return s
		}
		if upper && 'a' <= c && c <= 'z' {
			return canonicalMIMEHeaderKey([]byte(s))
		}
		if !upper && 'A' <= c && c <= 'Z' {
			return canonicalMIMEHeaderKey([]byte(s))
		}
		upper = c == '-'
	}
	return s
}

func IsTokenRune(r rune) bool {
	i := int(r)
	return i < len(isTokenTable) && isTokenTable[i]
}

func ValidHeaderFieldName(v string) bool {
	if len(v) == 0 {
		return false
	}
	for _, r := range v {
		if !IsTokenRune(r) {
			return false
		}
	}
	return true
}

func ValidHeaderFieldValue(v string) bool {
	for i := 0; i < len(v); i++ {
		b := v[i]
		if isCTL(b) && !isLWS(b) {
			return false
		}
	}
	return true
}
