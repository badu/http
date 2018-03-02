/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package url

type (
	// Error reports an error and the operation and URL that caused it.
	Error struct {
		Op  string
		URL string
		Err error
	}

	timeout interface {
		Timeout() bool
	}

	temporary interface {
		Temporary() bool
	}

	encoding int

	EscapeError string

	InvalidHostError string

	// A URL represents a parsed URL (technically, a URI reference).
	//
	// The general form represented is:
	//
	//	[scheme:][//[userinfo@]host][/]path[?query][#fragment]
	//
	// URLs that do not start with a slash after the scheme are interpreted as:
	//
	//	scheme:opaque[?query][#fragment]
	//
	// Note that the Path field is stored in decoded form: /%47%6f%2f becomes /Go/.
	// A consequence is that it is impossible to tell which slashes in the Path were
	// slashes in the raw URL and which were %2f. This distinction is rarely important,
	// but when it is, code must not use Path directly.
	// The Parse function sets both Path and RawPath in the URL it returns,
	// and URL's String method uses RawPath if it is a valid encoding of Path,
	// by calling the EscapedPath method.
	URL struct {
		Scheme     string
		Opaque     string    // encoded opaque data
		User       *Userinfo // username and password information
		Host       string    // host or host:port
		Path       string    // path (relative paths may omit leading slash)
		RawPath    string    // encoded path hint (see EscapedPath method)
		ForceQuery bool      // append a query ('?') even if RawQuery is empty
		RawQuery   string    // encoded query values, without '?'
		Fragment   string    // fragment for references, without '#'
	}

	// The Userinfo type is an immutable encapsulation of username and
	// password details for a URL. An existing Userinfo value is guaranteed
	// to have a username set (potentially empty, as allowed by RFC 2396),
	// and optionally a password.
	Userinfo struct {
		username    string
		password    string
		passwordSet bool
	}

	// Values maps a string key to a list of values.
	// It is typically used for query parameters and form values.
	// Unlike in the http.Header map, the keys in a Values map
	// are case-sensitive.
	Values map[string][]string
)

const (
	encodePath encoding = 1 + iota
	encodePathSegment
	encodeHost
	encodeZone
	encodeUserPassword
	encodeQueryComponent
	encodeFragment
)
