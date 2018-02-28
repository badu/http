/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package http

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"
)

// requestMethodUsuallyLacksBody reports whether the given request
// method is one that typically does not involve a request body.
// This is used by the Transport (via
// transferWriter.shouldSendChunkedRequestBody) to determine whether
// we try to test-read a byte from a non-nil Request.Body when
// Request.outgoingLength() returns -1. See the comments in
// shouldSendChunkedRequestBody.
func requestMethodUsuallyLacksBody(method string) bool {
	switch method {
	case GET, HEAD, DELETE, OPTIONS, PROPFIND, SEARCH:
		return true
	}
	return false
}

func noResponseBodyExpected(requestMethod string) bool {
	return requestMethod == HEAD
}

// bodyAllowedForStatus reports whether a given response status code
// permits a body. See RFC 2616, section 4.4.
func bodyAllowedForStatus(status int) bool {
	switch {
	case status >= 100 && status <= 199:
		return false
	case status == 204:
		return false
	case status == 304:
		return false
	}
	return true
}

func suppressedHeaders(status int) []string {
	switch {
	case status == 304:
		// RFC 2616 section 10.3.5: "the response MUST NOT include other entity-headers"
		return suppressedHeaders304
	case !bodyAllowedForStatus(status):
		return suppressedHeadersNoBody
	}
	return nil
}

// @comment : called from public_response.go ReadResponse function - used in transport and tests
func readTransferResponse(resp *Response, r *bufio.Reader) error {
	t := &transferReader{
		RequestMethod: GET,
		Header:        resp.Header,
		StatusCode:    resp.StatusCode,
		ProtoMajor:    resp.ProtoMajor,
		ProtoMinor:    resp.ProtoMinor,
		Close:         shouldClose(resp.ProtoMajor, resp.ProtoMinor, resp.Header, true),
	}

	if resp.Request != nil {
		t.RequestMethod = resp.Request.Method
	}

	// Default to HTTP/1.1
	if t.ProtoMajor == 0 && t.ProtoMinor == 0 {
		t.ProtoMajor, t.ProtoMinor = 1, 1
	}

	// Transfer encoding, content length
	err := t.fixTransferEncoding()
	if err != nil {
		return err
	}

	realLength, err := fixLength(true, t.StatusCode, t.RequestMethod, t.Header, t.TransferEncoding)
	if err != nil {
		return err
	}

	if t.RequestMethod == HEAD {
		if n, err := parseContentLength(t.Header.get(ContentLength)); err != nil {
			return err
		} else {
			t.ContentLength = n
		}
	} else {
		t.ContentLength = realLength
	}

	// Trailer
	t.Trailer, err = fixTrailer(t.Header, t.TransferEncoding)
	if err != nil {
		return err
	}

	// If there is no Content-Length or chunked Transfer-Encoding on a *Response
	// and the status is not 1xx, 204 or 304, then the body is unbounded.
	// See RFC 2616, section 4.4.
	if realLength == -1 &&
		!chunked(t.TransferEncoding) &&
		bodyAllowedForStatus(t.StatusCode) {
		// Unbounded body.
		t.Close = true
	}

	// Prepare body reader. ContentLength < 0 means chunked encoding
	// or close connection when finished, since multipart is not supported yet
	switch {
	case chunked(t.TransferEncoding):
		if noResponseBodyExpected(t.RequestMethod) {
			t.Body = NoBody
		} else {
			t.Body = &body{reader: &chunkedReader{r: r}, responseOrRequestIntf: resp, bufReader: r, isClosing: t.Close}
		}
	case realLength == 0:
		t.Body = NoBody
	case realLength > 0:
		t.Body = &body{reader: io.LimitReader(r, realLength), isClosing: t.Close}
	default:
		// realLength < 0, i.e. "Content-Length" not mentioned in header
		if t.Close {
			// Close semantics (i.e. HTTP/1.0)
			t.Body = &body{reader: r, isClosing: t.Close}
		} else {
			// Persistent connection (i.e. HTTP/1.1)
			t.Body = NoBody
		}
	}
	//TODO : @badu - maybe we should directly work with Body of response
	resp.Body = t.Body
	resp.ContentLength = t.ContentLength
	resp.TransferEncoding = t.TransferEncoding
	resp.Close = t.Close
	resp.Trailer = t.Trailer

	return nil
}

func readTransferRequest(req *Request, r *bufio.Reader) error {
	// Transfer semantics for Requests are exactly like those for
	// Responses with status code 200, responding to a GET method
	t := &transferReader{
		Header:        req.Header,
		RequestMethod: req.Method,
		ProtoMajor:    req.ProtoMajor,
		ProtoMinor:    req.ProtoMinor,
		StatusCode:    200,
		Close:         req.Close,
	}

	// Default to HTTP/1.1
	if t.ProtoMajor == 0 && t.ProtoMinor == 0 {
		t.ProtoMajor, t.ProtoMinor = 1, 1
	}

	// Transfer encoding, content length
	err := t.fixTransferEncoding()
	if err != nil {
		return err
	}

	realLength, err := fixLength(false, t.StatusCode, t.RequestMethod, t.Header, t.TransferEncoding)
	if err != nil {
		return err
	}
	t.ContentLength = realLength

	// Trailer
	t.Trailer, err = fixTrailer(t.Header, t.TransferEncoding)
	if err != nil {
		return err
	}

	// Prepare body reader. ContentLength < 0 means chunked encoding
	// or close connection when finished, since multipart is not supported yet
	switch {
	case chunked(t.TransferEncoding):
		if noResponseBodyExpected(t.RequestMethod) {
			t.Body = NoBody
		} else {
			t.Body = &body{reader: &chunkedReader{r: r}, responseOrRequestIntf: req, bufReader: r, isClosing: t.Close}
		}
	case realLength == 0:
		t.Body = NoBody
	case realLength > 0:
		t.Body = &body{reader: io.LimitReader(r, realLength), isClosing: t.Close}
	default:
		// realLength < 0, i.e. "Content-Length" not mentioned in header
		if t.Close {
			// Close semantics (i.e. HTTP/1.0)
			t.Body = &body{reader: r, isClosing: t.Close}
		} else {
			// Persistent connection (i.e. HTTP/1.1)
			t.Body = NoBody
		}
	}

	//TODO : @badu - maybe we should directly work with Body of request
	req.Body = t.Body
	req.ContentLength = t.ContentLength
	req.TransferEncoding = t.TransferEncoding
	req.Close = t.Close
	req.Trailer = t.Trailer

	return nil
}

// Checks whether chunked is part of the encodings stack
func chunked(te []string) bool { return len(te) > 0 && te[0] == DoChunked }

// Checks whether the encoding is explicitly "identity".
func isIdentity(te []string) bool { return len(te) == 1 && te[0] == DoIdentity }

// Determine the expected body length, using RFC 2616 Section 4.4. This
// function is not a method, because ultimately it should be shared by
// ReadResponse and ReadRequest.
func fixLength(isResponse bool, status int, requestMethod string, header Header, te []string) (int64, error) {
	isRequest := !isResponse
	contentLens := header[ContentLength]

	// Hardening against HTTP request smuggling
	if len(contentLens) > 1 {
		// Per RFC 7230 Section 3.3.2, prevent multiple
		// Content-Length headers if they differ in value.
		// If there are dups of the value, remove the dups.
		// See Issue 16490.
		first := strings.TrimSpace(contentLens[0])
		for _, ct := range contentLens[1:] {
			if first != strings.TrimSpace(ct) {
				return 0, fmt.Errorf("http: message cannot contain multiple Content-Length headers; got %q", contentLens)
			}
		}

		// deduplicate Content-Length
		header.Del(ContentLength)
		header.Add(ContentLength, first)

		contentLens = header[ContentLength]
	}

	// Logic based on response type or status
	if noResponseBodyExpected(requestMethod) {
		// For HTTP requests, as part of hardening against request
		// smuggling (RFC 7230), don't allow a Content-Length header for
		// methods which don't permit bodies. As an exception, allow
		// exactly one Content-Length header if its value is "0".
		if isRequest && len(contentLens) > 0 && !(len(contentLens) == 1 && contentLens[0] == "0") {
			return 0, fmt.Errorf("http: method cannot contain a Content-Length; got %q", contentLens)
		}
		return 0, nil
	}
	if status/100 == 1 {
		return 0, nil
	}
	switch status {
	case 204, 304:
		return 0, nil
	}

	// Logic based on Transfer-Encoding
	if chunked(te) {
		return -1, nil
	}

	// Logic based on Content-Length
	var cl string
	if len(contentLens) == 1 {
		cl = strings.TrimSpace(contentLens[0])
	}
	if cl != "" {
		n, err := parseContentLength(cl)
		if err != nil {
			return -1, err
		}
		return n, nil
	} else {
		header.Del(ContentLength)
	}

	if isRequest {
		// RFC 2616 neither explicitly permits nor forbids an
		// entity-body on a GET request so we permit one if
		// declared, but we default to 0 here (not -1 below)
		// if there's no mention of a body.
		// Likewise, all other request methods are assumed to have
		// no body if neither Transfer-Encoding chunked nor a
		// Content-Length are set.
		return 0, nil
	}

	// Body-EOF logic based on other methods (like closing, or chunked coding)
	return -1, nil
}

// Determine whether to hang up after sending a request and body, or
// receiving a response and body
// 'header' is the request headers
func shouldClose(major, minor int, header Header, removeCloseHeader bool) bool {
	if major < 1 {
		return true
	}

	conv := header[Connection]
	hasClose := headersValuesContainsToken(conv, DoClose)
	if major == 1 && minor == 0 {
		return hasClose || !headersValuesContainsToken(conv, DoKeepAlive)
	}

	if hasClose && removeCloseHeader {
		header.Del(Connection)
	}

	return hasClose
}

// HeaderValuesContainsToken reports whether any string in values
// contains the provided token, ASCII case-insensitively.
func headersValuesContainsToken(values []string, token string) bool {
	for _, v := range values {
		if headerValueContainsToken(v, token) {
			return true
		}
	}
	return false
}

func headerValueContainsToken(v string, token string) bool {
	v = trimOWS(v)
	if comma := strings.IndexByte(v, ','); comma != -1 {
		return tokenEqual(trimOWS(v[:comma]), token) || headerValueContainsToken(v[comma+1:], token)
	}
	return tokenEqual(v, token)
}

func isOWS(b byte) bool { return b == ' ' || b == '\t' }

func trimOWS(x string) string {
	// TODO: consider using strings.Trim(x, " \t") instead,
	// if and when it's fast enough. See issue 10292.
	// But this ASCII-only code will probably always beat UTF-8
	// aware code.
	for len(x) > 0 && isOWS(x[0]) {
		x = x[1:]
	}
	for len(x) > 0 && isOWS(x[len(x)-1]) {
		x = x[:len(x)-1]
	}
	return x
}

func tokenEqual(t1, t2 string) bool {
	if len(t1) != len(t2) {
		return false
	}
	for i, b := range t1 {
		if b >= utf8.RuneSelf {
			// No UTF-8 or non-ASCII allowed in tokens.
			return false
		}
		if lowerASCII(byte(b)) != lowerASCII(t2[i]) {
			return false
		}
	}
	return true
}

func lowerASCII(b byte) byte {
	if 'A' <= b && b <= 'Z' {
		return b + ('a' - 'A')
	}
	return b
}

// Parse the trailer header
func fixTrailer(header Header, te []string) (Header, error) {
	vv, ok := header[Trailer]
	if !ok {
		return nil, nil
	}
	header.Del(Trailer)

	trailer := make(Header)
	var err error
	for _, v := range vv {
		foreachHeaderElement(v, func(key string) {
			key = CanonicalHeaderKey(key)
			switch key {
			case TransferEncoding, Trailer, ContentLength:
				if err == nil {
					err = &badStringError{"bad trailer key", key}
					return
				}
			}
			trailer[key] = nil
		})
	}
	if err != nil {
		return nil, err
	}
	if len(trailer) == 0 {
		return nil, nil
	}
	if !chunked(te) {
		// Trailer and no chunking
		return nil, ErrUnexpectedTrailer
	}
	return trailer, nil
}

func seeUpcomingDoubleCRLF(r *bufio.Reader) bool {
	for peekSize := 4; ; peekSize++ {
		// This loop stops when Peek returns an error,
		// which it does when r's buffer has been filled.
		buf, err := r.Peek(peekSize)
		if bytes.HasSuffix(buf, doubleCRLF) {
			return true
		}
		if err != nil {
			break
		}
	}
	return false
}

func mergeSetHeader(dst *Header, src Header) {
	if *dst == nil {
		*dst = src
		return
	}
	for k, vv := range src {
		(*dst)[k] = vv
	}
}

// parseContentLength trims whitespace from s and returns -1 if no value
// is set, or the value if it's >= 0.
func parseContentLength(cl string) (int64, error) {
	cl = strings.TrimSpace(cl)
	if cl == "" {
		return -1, nil
	}
	n, err := strconv.ParseInt(cl, 10, 64)
	if err != nil || n < 0 {
		return 0, &badStringError{"bad Content-Length", cl}
	}
	return n, nil

}
