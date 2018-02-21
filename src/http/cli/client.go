/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net/url"
	"sort"
	"strings"

	. "http"
)

func (c *Client) send(req *Request) (resp *Response, err error) {
	if c.Jar != nil {
		for _, cookie := range c.Jar.Cookies(req.URL) {
			AddCookie(cookie, req)
		}
	}
	resp, err = send(req, c.transport())
	if err != nil {
		return nil, err
	}
	if c.Jar != nil {
		if rc := RespCookies(resp); len(rc) > 0 {
			c.Jar.SetCookies(req.URL, rc)
		}
	}
	return resp, nil
}

func (c *Client) transport() RoundTripper {
	if c.Transport != nil {
		return c.Transport
	}
	return DefaultTransport
}

// Get issues a GET to the specified URL. If the response is one of the
// following redirect codes, Get follows the redirect after calling the
// Client's CheckRedirect function:
//
//    301 (Moved Permanently)
//    302 (Found)
//    303 (See Other)
//    307 (Temporary Redirect)
//    308 (Permanent Redirect)
//
// An error is returned if the Client's CheckRedirect function fails
// or if there was an HTTP protocol error. A non-2xx response doesn't
// cause an error.
//
// When err is nil, resp always contains a non-nil resp.Body.
// Caller should close resp.Body when done reading from it.
//
// To make a request with custom headers, use NewRequest and Client.Do.
func (c *Client) Get(url string) (resp *Response, err error) {
	req, err := NewRequest(GET, url, nil)
	if err != nil {
		return nil, err
	}
	return c.Do(req)
}

//@comment : added for easy access
func (c *Client) GetWithContext(url string, ctx context.Context) (resp *Response, err error) {
	req, err := NewRequest(GET, url, nil)
	if err != nil {
		return nil, err
	}
	req.WithContext(ctx)
	return c.Do(req)
}

// checkRedirect calls either the user's configured CheckRedirect
// function, or the default.
func (c *Client) checkRedirect(req *Request, via []*Request) error {
	fn := c.CheckRedirect
	if fn == nil {
		fn = defaultCheckRedirect
	}
	return fn(req, via)
}

// Do sends an HTTP request and returns an HTTP response, following
// policy (such as redirects, cookies, auth) as configured on the
// client.
//
// An error is returned if caused by client policy (such as
// CheckRedirect), or failure to speak HTTP (such as a network
// connectivity problem). A non-2xx status code doesn't cause an
// error.
//
// If the returned error is nil, the Response will contain a non-nil
// Body which the user is expected to close. If the Body is not
// closed, the Client's underlying RoundTripper (typically Transport)
// may not be able to re-use a persistent TCP connection to the server
// for a subsequent "keep-alive" request.
//
// The request Body, if non-nil, will be closed by the underlying
// Transport, even on errors.
//
// On error, any Response can be ignored. A non-nil Response with a
// non-nil error only occurs when CheckRedirect fails, and even then
// the returned Response.Body is already closed.
//
// Generally Get, Post, or PostForm will be used instead of Do.
//
// If the server replies with a redirect, the Client first uses the
// CheckRedirect function to determine whether the redirect should be
// followed. If permitted, a 301, 302, or 303 redirect causes
// subsequent requests to use HTTP method GET
// (or HEAD if the original request was HEAD), with no body.
// A 307 or 308 redirect preserves the original HTTP method and body,
// provided that the Request.GetBody function is defined.
// The NewRequest function automatically sets GetBody for common
// standard library body types.
func (c *Client) Do(req *Request) (*Response, error) {
	if req.URL == nil {
		if req.Body != nil {
			req.Body.Close()
		}
		return nil, errors.New("http: nil Request.URL")
	}

	var (
		reqs          []*Request
		resp          *Response
		copyHeaders   = c.makeHeadersCopier(req)
		reqBodyClosed = false // have we closed the current req.Body?

		// Redirect behavior:
		redirectMethod string
		includeBody    bool
	)
	uerr := func(err error) error {
		// the body may have been closed already by c.send()
		if !reqBodyClosed {
			if req.Body != nil {
				req.Body.Close()
			}
		}
		method := valueOrDefault(reqs[0].Method, GET)
		var urlStr string
		if resp != nil && resp.Request != nil {
			urlStr = resp.Request.URL.String()
		} else {
			urlStr = req.URL.String()
		}
		return &url.Error{
			Op:  method[:1] + strings.ToLower(method[1:]),
			URL: urlStr,
			Err: err,
		}
	}
	for {
		// For all but the first request, create the next
		// request hop and replace req.
		if len(reqs) > 0 {
			loc := resp.Header.Get(Location)
			if loc == "" {
				if resp.Body != nil {
					resp.Body.Close()
				}
				return nil, uerr(fmt.Errorf("%d response missing Location header", resp.StatusCode))
			}
			u, err := req.URL.Parse(loc)
			if err != nil {
				if resp.Body != nil {
					resp.Body.Close()
				}
				return nil, uerr(fmt.Errorf("failed to parse Location header %q: %v", loc, err))
			}
			host := ""
			if req.Host != "" && req.Host != req.URL.Host {
				// If the caller specified a custom Host header and the
				// redirect location is relative, preserve the Host header
				// through the redirect. See issue #22233.
				if u, _ := url.Parse(loc); u != nil && !u.IsAbs() {
					host = req.Host
				}
			}
			ireq := reqs[0]
			req = &Request{
				Method:   redirectMethod,
				Response: resp,
				URL:      u,
				Header:   make(Header),
				Host:     host,
			}
			req.SetCtx(ireq.Context())
			if includeBody && ireq.GetBody != nil {
				req.Body, err = ireq.GetBody()
				if err != nil {
					if resp.Body != nil {
						resp.Body.Close()
					}
					return nil, uerr(err)
				}
				req.ContentLength = ireq.ContentLength
			}

			// Copy original headers before setting the Referer,
			// in case the user set Referer on their first request.
			// If they really want to override, they can do it in
			// their CheckRedirect func.
			copyHeaders(req)

			// Add the Referer header from the most recent
			// request URL to the new one, if it's not https->http:
			if ref := refererForURL(reqs[len(reqs)-1].URL, req.URL); ref != "" {
				req.Header.Set(Referer, ref)
			}
			err = c.checkRedirect(req, reqs)

			// Sentinel error to let users select the
			// previous response, without closing its
			// body. See Issue 10069.
			if err == ErrUseLastResponse {
				return resp, nil
			}

			// Close the previous response's body. But
			// read at least some of the body so if it's
			// small the underlying TCP connection will be
			// re-used. No need to check for errors: if it
			// fails, the Transport won't reuse it anyway.
			const maxBodySlurpSize = 2 << 10
			if resp.ContentLength == -1 || resp.ContentLength <= maxBodySlurpSize {
				io.CopyN(ioutil.Discard, resp.Body, maxBodySlurpSize)
			}
			resp.Body.Close()

			if err != nil {
				// Special case for Go 1 compatibility: return both the response
				// and an error if the CheckRedirect function failed.
				// See https://golang.org/issue/3795
				// The resp.Body has already been closed.
				ue := uerr(err)
				ue.(*url.Error).URL = loc
				return resp, ue
			}
		}

		reqs = append(reqs, req)
		var err error

		if resp, err = c.send(req); err != nil {
			reqBodyClosed = true
			return nil, uerr(err)
		}

		var shouldRedirect bool
		redirectMethod, shouldRedirect, includeBody = redirectBehavior(req.Method, resp, reqs[0])
		if !shouldRedirect {
			return resp, nil
		}
		if req.Body != nil {
			req.Body.Close()
		}
	}
}

// makeHeadersCopier makes a function that copies headers from the
// initial Request, ireq. For every redirect, this function must be called
// so that it can copy headers into the upcoming Request.
func (c *Client) makeHeadersCopier(ireq *Request) func(*Request) {
	// The headers to copy are from the very initial request.
	// We use a closured callback to keep a reference to these original headers.
	var (
		ireqhdr  = ireq.Header.Clone()
		icookies map[string][]*Cookie
	)
	if c.Jar != nil && ireq.Header.Get("Cookie") != "" {
		icookies = make(map[string][]*Cookie)
		for _, c := range ReqCookies(ireq) {
			icookies[c.Name] = append(icookies[c.Name], c)
		}
	}

	preq := ireq // The previous request
	return func(req *Request) {
		// If Jar is present and there was some initial cookies provided
		// via the request header, then we may need to alter the initial
		// cookies as we follow redirects since each redirect may end up
		// modifying a pre-existing cookie.
		//
		// Since cookies already set in the request header do not contain
		// information about the original domain and path, the logic below
		// assumes any new set cookies override the original cookie
		// regardless of domain or path.
		//
		// See https://golang.org/issue/17494
		if c.Jar != nil && icookies != nil {
			var changed bool
			resp := req.Response // The response that caused the upcoming redirect
			for _, c := range RespCookies(resp) {
				if _, ok := icookies[c.Name]; ok {
					delete(icookies, c.Name)
					changed = true
				}
			}
			if changed {
				ireqhdr.Del("Cookie")
				var ss []string
				for _, cs := range icookies {
					for _, c := range cs {
						ss = append(ss, c.Name+"="+c.Value)
					}
				}
				sort.Strings(ss) // Ensure deterministic headers
				ireqhdr.Set("Cookie", strings.Join(ss, "; "))
			}
		}

		// Copy the initial request's Header values
		// (at least the safe ones).
		for k, vv := range ireqhdr {
			if shouldCopyHeaderOnRedirect(k, preq.URL, req.URL) {
				req.Header[k] = vv
			}
		}

		preq = req // Update previous Request with the current request
	}
}

// Post issues a POST to the specified URL.
//
// Caller should close resp.Body when done reading from it.
//
// If the provided body is an io.Closer, it is closed after the
// request.
//
// To set custom headers, use NewRequest and Client.Do.
//
// See the Client.Do method documentation for details on how redirects
// are handled.
func (c *Client) Post(url string, contentType string, body io.Reader) (resp *Response, err error) {
	req, err := NewRequest(POST, url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set(ContentType, contentType)
	return c.Do(req)
}

// PostForm issues a POST to the specified URL,
// with data's keys and values URL-encoded as the request body.
//
// The Content-Type header is set to application/x-www-form-urlencoded.
// To set other headers, use NewRequest and DefaultClient.Do.
//
// When err is nil, resp always contains a non-nil resp.Body.
// Caller should close resp.Body when done reading from it.
//
// See the Client.Do method documentation for details on how redirects
// are handled.
func (c *Client) PostForm(url string, data url.Values) (resp *Response, err error) {
	return c.Post(url, "application/x-www-form-urlencoded", strings.NewReader(data.Encode()))
}

// Head issues a HEAD to the specified URL. If the response is one of the
// following redirect codes, Head follows the redirect after calling the
// Client's CheckRedirect function:
//
//    301 (Moved Permanently)
//    302 (Found)
//    303 (See Other)
//    307 (Temporary Redirect)
//    308 (Permanent Redirect)
func (c *Client) Head(url string) (resp *Response, err error) {
	req, err := NewRequest("HEAD", url, nil)
	if err != nil {
		return nil, err
	}
	return c.Do(req)
}
