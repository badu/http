/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package cli

import (
	"fmt"
	"io"
	"net/url"

	. "http"
)

//TODO : @badu - exported so tests can access it
func RefererForURL(lastReq, newReq *url.URL) string {
	return refererForURL(lastReq, newReq)
}

// Get issues a GET to the specified URL. If the response is one of
// the following redirect codes, Get follows the redirect, up to a
// maximum of 10 redirects:
//
//    301 (Moved Permanently)
//    302 (Found)
//    303 (See Other)
//    307 (Temporary Redirect)
//    308 (Permanent Redirect)
//
// An error is returned if there were too many redirects or if there
// was an HTTP protocol error. A non-2xx response doesn't cause an
// error.
//
// When err is nil, resp always contains a non-nil resp.Body.
// Caller should close resp.Body when done reading from it.
//
// Get is a wrapper around DefaultClient.Get.
//
// To make a request with custom headers, use NewRequest and
// DefaultClient.Do.
func Get(url string) (resp *Response, err error) {
	return DefaultClient.Get(url)
}

// Post issues a POST to the specified URL.
//
// Caller should close resp.Body when done reading from it.
//
// If the provided body is an io.Closer, it is closed after the
// request.
//
// Post is a wrapper around DefaultClient.Post.
//
// To set custom headers, use NewRequest and DefaultClient.Do.
//
// See the Client.Do method documentation for details on how redirects
// are handled.
func Post(url string, contentType string, body io.Reader) (resp *Response, err error) {
	return DefaultClient.Post(url, contentType, body)
}

// PostForm issues a POST to the specified URL, with data's keys and
// values URL-encoded as the request body.
//
// The Content-Type header is set to application/x-www-form-urlencoded.
// To set other headers, use NewRequest and DefaultClient.Do.
//
// When err is nil, resp always contains a non-nil resp.Body.
// Caller should close resp.Body when done reading from it.
//
// PostForm is a wrapper around DefaultClient.PostForm.
//
// See the Client.Do method documentation for details on how redirects
// are handled.
//noinspection GoUnusedExportedFunction
func PostForm(url string, data url.Values) (resp *Response, err error) {
	return DefaultClient.PostForm(url, data)
}

// Head issues a HEAD to the specified URL. If the response is one of
// the following redirect codes, Head follows the redirect, up to a
// maximum of 10 redirects:
//
//    301 (Moved Permanently)
//    302 (Found)
//    303 (See Other)
//    307 (Temporary Redirect)
//    308 (Permanent Redirect)
//
// Head is a wrapper around DefaultClient.Head
//noinspection GoUnusedExportedFunction
func Head(url string) (resp *Response, err error) {
	return DefaultClient.Head(url)
}

//TODO : @badu - exported for tests
func ShouldCopyHeaderOnRedirect(headerKey string, initial, dest *url.URL) bool {
	return shouldCopyHeaderOnRedirect(headerKey, initial, dest)
}

//===========================
// Cookies
//===========================

// New returns a new cookie jar. A nil *Options is equivalent to a zero
// Options.
func NewCookie(o *Options) (*Jar, error) {
	jar := &Jar{
		entries: make(map[string]map[string]cookieEntry),
	}
	if o != nil {
		jar.psList = o.PublicSuffixList
	}
	return jar, nil
}

//===========================
// Extracted receivers of Request
//===========================
// Cookies parses and returns the HTTP cookies sent with the request.
func ReqCookies(fromReq *Request) []*Cookie {
	return readCookies(fromReq.Header, "")
}

// Cookies parses and returns the cookies set in the Set-Cookie headers.
func RespCookies(fromResp *Response) []*Cookie {
	return readSetCookies(fromResp.Header)
}

// Cookie returns the named cookie provided in the request or
// ErrNoCookie if not found.
// If multiple cookies match the given name, only one cookie will
// be returned.
func GetCookie(name string, fromReq *Request) (*Cookie, error) {
	for _, c := range readCookies(fromReq.Header, name) {
		return c, nil
	}
	return nil, ErrNoCookie
}

// AddCookie adds a cookie to the request. Per RFC 6265 section 5.4,
// AddCookie does not attach more than one Cookie header field. That
// means all cookies, if any, are written into the same line,
// separated by semicolon.
func AddCookie(c *Cookie, toReq *Request) {
	s := fmt.Sprintf("%s=%s", sanitizeCookieName(c.Name), sanitizeCookieValue(c.Value))
	if c := toReq.Header.Get(CookieHeader); c != "" {
		toReq.Header.Set(CookieHeader, c+"; "+s)
	} else {
		toReq.Header.Set(CookieHeader, s)
	}
}

// SetCookie adds a Set-Cookie header to the provided ResponseWriter's headers.
// The provided cookie must have a valid Name. Invalid cookies may be
// silently dropped.
func SetCookie(w ResponseWriter, cookie *Cookie) {
	if v := cookie.String(); v != "" {
		w.Header().Add(SetCookieHeader, v)
	}
}
