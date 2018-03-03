/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package hdr

func isASCIISpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r'
}

func isASCIILetter(b byte) bool {
	b |= 0x20 // make lower case
	return 'a' <= b && b <= 'z'
}

// trim returns s with leading and trailing spaces and tabs removed.
// It does not assume Unicode or UTF-8.
func trim(s []byte) []byte {
	i := 0
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	n := len(s)
	for n > i && (s[n-1] == ' ' || s[n-1] == '\t') {
		n--
	}
	return s[i:n]
}

// validHeaderFieldByte reports whether b is a valid byte in a header
// field name. RFC 7230 says:
//   header-field   = field-name ":" OWS field-value OWS
//   field-name     = token
//   tchar = "!" / "#" / "$" / "%" / "&" / "'" / "*" / "+" / "-" / "." /
//           "^" / "_" / "`" / "|" / "~" / DIGIT / ALPHA
//   token = 1*tchar
func validHeaderFieldByte(b byte) bool {
	return int(b) < len(isTokenTable) && isTokenTable[b]
}

// canonicalMIMEHeaderKey is like CanonicalHeaderKey but is
// allowed to mutate the provided byte slice before returning the
// string.
//
// For invalid inputs (if a contains spaces or non-token bytes), a
// is unchanged and a string copy is returned.
func canonicalMIMEHeaderKey(a []byte) string {
	// See if a looks like a header key. If not, return it unchanged.
	for _, c := range a {
		if validHeaderFieldByte(c) {
			continue
		}
		// Don't canonicalize.
		return string(a)
	}

	upper := true
	for i, c := range a {
		// Canonicalize: first letter upper case
		// and upper case after each dash.
		// (Host, User-Agent, If-Modified-Since).
		// MIME headers are ASCII only, so no Unicode issues.
		if upper && 'a' <= c && c <= 'z' {
			c -= toLower
		} else if !upper && 'A' <= c && c <= 'Z' {
			c += toLower
		}
		a[i] = c
		upper = c == '-' // for next time
	}
	// The compiler recognizes m[string(byteSlice)] as a special
	// case, so a copy of a's bytes into a new string does not
	// happen in this map lookup:
	if v := commonHeader[string(a)]; v != "" {
		return v
	}
	return string(a)
}

func isLWS(b byte) bool { return b == ' ' || b == '\t' }

func isCTL(b byte) bool {
	const del = 0x7f // a CTL
	return b < ' ' || b == del
}

func init() {
	for _, v := range []string{
		Accept,
		AcceptCharset,
		AcceptEncoding,
		AcceptLanguage,
		AcceptRanges,
		Authorization,
		CacheControl,
		Cc,
		Connection,
		ContentEncoding,
		ContentId,
		ContentLanguage,
		ContentLength,
		ContentRange,
		ContentTransferEncoding,
		ContentType,
		CookieHeader,
		Date,
		DkimSignature,
		Etag,
		Expires,
		Expect,
		From,
		Host,
		IfModifiedSince,
		IfNoneMatch,
		InReplyTo,
		LastModified,
		Location,
		MessageId,
		MimeVersion,
		Pragma,
		Received,
		Referer,
		ReturnPath,
		ServerHeader,
		SetCookieHeader,
		Subject,
		TransferEncoding,
		To,
		Trailer,
		UpgradeHeader,
		UserAgent,
		Via,
		XForwardedFor,
		XImforwards,
		XPoweredBy,
	} {
		commonHeader[v] = v
	}
}
