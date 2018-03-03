/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package http

import (
	"bytes"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/badu/http/hdr"
	"github.com/badu/http/url"
)

// Location returns the URL of the response's Location header,
// if present. Relative redirects are resolved relative to
// the Response's Request. ErrNoLocation is returned if no
// Location header is present.
func (r *Response) Location() (*url.URL, error) {
	lv := r.Header.Get(hdr.Location)
	if lv == "" {
		return nil, ErrNoLocation
	}
	if r.Request != nil && r.Request.URL != nil {
		return r.Request.URL.Parse(lv)
	}
	return url.Parse(lv)
}

// ProtoAtLeast reports whether the HTTP protocol used
// in the response is at least major.minor.
func (r *Response) ProtoAtLeast(major, minor int) bool {
	return r.ProtoMajor > major ||
		r.ProtoMajor == major && r.ProtoMinor >= minor
}

// Write writes r to w in the HTTP/1.x server response format,
// including the status line, headers, body, and optional trailer.
//
// This method consults the following fields of the response r:
//
//  StatusCode
//  ProtoMajor
//  ProtoMinor
//  Request.Method
//  TransferEncoding
//  Trailer
//  Body
//  ContentLength
//  Header, values for non-canonical keys will have unpredictable behavior
//
// The Response Body is closed after it is sent.
func (r *Response) Write(w io.Writer) error {
	// Status line
	text := r.Status
	if text == "" {
		var ok bool
		text, ok = statusText[r.StatusCode]
		if !ok {
			text = "status code " + strconv.Itoa(r.StatusCode)
		}
	} else {
		// Just to reduce stutter, if user set r.Status to "200 OK" and StatusCode to 200.
		// Not important.
		text = strings.TrimPrefix(text, strconv.Itoa(r.StatusCode)+" ")
	}

	if _, err := fmt.Fprintf(w, "HTTP/%d.%d %03d %s\r\n", r.ProtoMajor, r.ProtoMinor, r.StatusCode, text); err != nil {
		return err
	}

	// Clone it, so we can modify r1 as needed.
	r1 := new(Response)
	//TODO : @badu - what an ugly way to clone
	*r1 = *r
	if r1.ContentLength == 0 && r1.Body != nil {
		// Is it actually 0 length? Or just unknown?
		var buf [1]byte
		n, err := r1.Body.Read(buf[:])
		if err != nil && err != io.EOF {
			return err
		}
		if n == 0 {
			// Reset it to a known zero reader, in case underlying one
			// is unhappy being read repeatedly.
			r1.Body = NoBody
		} else {
			r1.ContentLength = -1
			r1.Body = struct {
				io.Reader
				io.Closer
			}{
				io.MultiReader(bytes.NewReader(buf[:1]), r.Body),
				r.Body,
			}
		}
	}
	// If we're sending a non-chunked HTTP/1.1 response without a
	// content-length, the only way to do that is the old HTTP/1.0
	// way, by noting the EOF with a connection close, so we need
	// to set Close.
	if r1.ContentLength == -1 && !r1.Close && r1.ProtoAtLeast(1, 1) && !chunked(r1.TransferEncoding) && !r1.Uncompressed {
		r1.Close = true
	}

	// Process Body,ContentLength,Close,Trailer
	tw, err := r1.createWriter()
	if err != nil {
		return err
	}

	//TODO : @badu - maybe move code below into createWriter()
	err = tw.WriteHeader(w)
	if err != nil {
		return err
	}

	// Rest of header
	err = r.Header.WriteSubset(w, respExcludeHeader)
	if err != nil {
		return err
	}

	// contentLengthAlreadySent may have been already sent for
	// POST/PUT requests, even if zero length. See Issue 8180.
	contentLengthAlreadySent := tw.shouldSendContentLength()
	if r1.ContentLength == 0 && !chunked(r1.TransferEncoding) && !contentLengthAlreadySent && bodyAllowedForStatus(r.StatusCode) {
		if _, err := io.WriteString(w, "Content-Length: 0\r\n"); err != nil {
			return err
		}
	}

	// End-of-header
	if _, err := io.WriteString(w, "\r\n"); err != nil {
		return err
	}

	// Write body and trailer
	err = tw.WriteBody(w)
	if err != nil {
		return err
	}

	// Success
	return nil
}
func (r *Response) createWriter() (*transferWriter, error) {
	t := &transferWriter{
		Body:             r.Body,
		BodyCloser:       r.Body,
		ContentLength:    r.ContentLength,
		Close:            r.Close,
		TransferEncoding: r.TransferEncoding,
		Header:           r.Header,
		Trailer:          r.Trailer,
		IsResponse:       true,
	}

	if r.Request != nil {
		t.Method = r.Request.Method
	}
	t.ResponseToHEAD = noResponseBodyExpected(t.Method)
	atLeastHTTP11 := r.ProtoAtLeast(1, 1)

	// Sanitize Body, ContentLength, TransferEncoding
	if t.ResponseToHEAD {
		t.Body = nil
		if chunked(t.TransferEncoding) {
			t.ContentLength = -1
		}
	} else {
		if !atLeastHTTP11 || t.Body == nil {
			t.TransferEncoding = nil
		}
		if chunked(t.TransferEncoding) {
			t.ContentLength = -1
		} else if t.Body == nil { // no chunking, no body
			t.ContentLength = 0
		}
	}

	// Sanitize Trailer
	if !chunked(t.TransferEncoding) {
		t.Trailer = nil
	}

	return t, nil
}

// @comment : decided to go public with this function - called everywhere
func (r *Response) CloseBody() {
	if r.Body != nil {
		//TODO : @badu - closer returns an error : why we don't handle it?
		r.Body.Close()
	}
}
