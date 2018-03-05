/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package util

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"strings"

	. "github.com/badu/http"
	"github.com/badu/http/hdr"
	. "github.com/badu/http/tport"
	"github.com/badu/http/url"
)

// drainBody reads all of b to memory and then returns two equivalent
// ReadClosers yielding the same bytes.
//
// It returns an error if the initial slurp of all bytes fails. It does not attempt
// to make the returned ReadClosers have identical error-matching behavior.
func drainBody(b io.ReadCloser) (r1, r2 io.ReadCloser, err error) {
	if b == NoBody {
		// No copying needed. Preserve the magic sentinel meaning of NoBody.
		return NoBody, NoBody, nil
	}
	var buf bytes.Buffer
	if _, err = buf.ReadFrom(b); err != nil {
		return nil, b, err
	}
	if err = b.Close(); err != nil {
		return nil, b, err
	}
	return ioutil.NopCloser(&buf), ioutil.NopCloser(bytes.NewReader(buf.Bytes())), nil
}

// DumpRequestOut is like DumpRequest but for outgoing client requests. It
// includes any headers that the standard Transport adds, such as
// User-Agent.
func DumpRequestOut(r *Request, body bool) ([]byte, error) {
	save := r.Body
	dummyBody := false
	if !body || r.Body == nil {
		r.Body = nil
		if r.ContentLength != 0 {
			r.Body = ioutil.NopCloser(io.LimitReader(neverEnding('x'), r.ContentLength))
			dummyBody = true
		}
	} else {
		var err error
		save, r.Body, err = drainBody(r.Body)
		if err != nil {
			return nil, err
		}
	}

	// Since we're using the actual Transport code to write the request,
	// switch to http so the Transport doesn't try to do an SSL
	// negotiation with our dumpConn and its bytes.Buffer & pipe.
	// The wire format for https and http are the same, anyway.
	reqSend := r
	if r.URL.Scheme == HTTPS {
		reqSend = new(Request)
		*reqSend = *r
		reqSend.URL = new(url.URL)
		*reqSend.URL = *r.URL
		reqSend.URL.Scheme = HTTP
	}

	// Use the actual Transport code to record what we would send
	// on the wire, but not using TCP.  Use a Transport with a
	// custom dialer that returns a fake net.Conn that waits
	// for the full input (and recording it), and then responds
	// with a dummy response.
	var buf bytes.Buffer // records the output
	pr, pw := io.Pipe()
	defer pr.Close()
	defer pw.Close()
	dr := &delegateReader{c: make(chan io.Reader)}

	t := &Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return &dumpConn{io.MultiWriter(&buf, pw), dr}, nil
		},
	}
	defer t.CloseIdleConnections()

	// Wait for the request before replying with a dummy response:
	go func() {
		req, err := ReadRequest(bufio.NewReader(pr))
		if err == nil {
			// Ensure all the body is read; otherwise
			// we'll get a partial dump.
			io.Copy(ioutil.Discard, req.Body)
			req.CloseBody()
		}
		dr.c <- strings.NewReader("HTTP/1.1 204 No Content\r\nConnection: close\r\n\r\n")
	}()

	_, err := t.RoundTrip(reqSend)

	r.Body = save
	if err != nil {
		return nil, err
	}
	dump := buf.Bytes()

	// If we used a dummy body above, remove it now.
	// TODO: if the r.ContentLength is large, we allocate memory
	// unnecessarily just to slice it off here. But this is just
	// a debug function, so this is acceptable for now. We could
	// discard the body earlier if this matters.
	if dummyBody {
		if i := bytes.Index(dump, doubleCRLF); i >= 0 {
			dump = dump[:i+4]
		}
	}
	return dump, nil
}

// DumpRequest returns the given request in its HTTP/1.x wire
// representation. It should only be used by servers to debug client
// requests. The returned representation is an approximation only;
// some details of the initial request are lost while parsing it into
// an Request. In particular, the order and case of header field
// names are lost. The order of values in multi-valued headers is kept
// intact. HTTP/2 requests are dumped in HTTP/1.x form, not in their
// original binary representations.
//
// If body is true, DumpRequest also returns the body. To do so, it
// consumes req.Body and then replaces it with a new io.ReadCloser
// that yields the same bytes. If DumpRequest returns an error,
// the state of req is undefined.
//
// The documentation for Request.Write details which fields
// of req are included in the dump.
//noinspection GoUnusedExportedFunction
func DumpRequest(req *Request, body bool) ([]byte, error) {
	var err error
	save := req.Body
	if !body || req.Body == nil {
		req.Body = nil
	} else {
		save, req.Body, err = drainBody(req.Body)
		if err != nil {
			return nil, err
		}
	}

	var b bytes.Buffer

	// By default, print out the unmodified req.RequestURI, which
	// is always set for incoming server requests. But because we
	// previously used req.URL.RequestURI and the docs weren't
	// always so clear about when to use DumpRequest vs
	// DumpRequestOut, fall back to the old way if the caller
	// provides a non-server Request.
	reqURI := req.RequestURI
	if reqURI == "" {
		reqURI = req.URL.RequestURI()
	}

	fmt.Fprintf(&b, "%s %s HTTP/%d.%d\r\n", ValueOrDefault(req.Method, GET), reqURI, req.ProtoMajor, req.ProtoMinor)

	//@comment : was `absRequestURI := strings.HasPrefix(req.RequestURI, HttpUrlPrefix) || strings.HasPrefix(req.RequestURI, HttpsUrlPrefix)`
	absRequestURI := (len(req.RequestURI) >= 7 && req.RequestURI[:7] == HttpUrlPrefix) || (len(req.RequestURI) >= 8 && req.RequestURI[:8] == HttpsUrlPrefix)
	if !absRequestURI {
		host := req.Host
		if host == "" && req.URL != nil {
			host = req.URL.Host
		}
		if host != "" {
			fmt.Fprintf(&b, "Host: %s\r\n", host)
		}
	}

	chunked := len(req.TransferEncoding) > 0 && req.TransferEncoding[0] == DoChunked
	if len(req.TransferEncoding) > 0 {
		fmt.Fprintf(&b, "Transfer-Encoding: %s\r\n", strings.Join(req.TransferEncoding, ","))
	}
	if req.Close {
		fmt.Fprintf(&b, "Connection: close\r\n")
	}

	err = req.Header.WriteSubset(&b, reqWriteExcludeHeaderDump)
	if err != nil {
		return nil, err
	}

	io.WriteString(&b, "\r\n") //TODO : maybe ? w.Write(CrLf) - If w implements a WriteString method, it is invoked directly. Otherwise, w.Write is called exactly once.

	if req.Body != nil {
		var dest io.Writer = &b
		if chunked {
			dest = NewChunkedWriter(dest)
		}
		_, err = io.Copy(dest, req.Body)
		if chunked {
			dest.(io.Closer).Close()
			io.WriteString(&b, "\r\n") //TODO : maybe ? w.Write(CrLf) - If w implements a WriteString method, it is invoked directly. Otherwise, w.Write is called exactly once.
		}
	}

	req.Body = save
	if err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// DumpResponse is like DumpRequest but dumps a response.
func DumpResponse(resp *Response, body bool) ([]byte, error) {
	var b bytes.Buffer
	var err error
	save := resp.Body
	savecl := resp.ContentLength

	if !body {
		// For content length of zero. Make sure the body is an empty
		// reader, instead of returning error through failureToReadBody{}.
		if resp.ContentLength == 0 {
			resp.Body = emptyBody
		} else {
			resp.Body = failureToReadBody{}
		}
	} else if resp.Body == nil {
		resp.Body = emptyBody
	} else {
		save, resp.Body, err = drainBody(resp.Body)
		if err != nil {
			return nil, err
		}
	}
	err = resp.Write(&b)
	if err == errNoBody {
		err = nil
	}
	resp.Body = save
	resp.ContentLength = savecl
	if err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func singleJoiningSlash(a, b string) string {
	aslash := len(a) >= 1 && a[len(a)-1:] == "/" // @comment : was `strings.HasSuffix(a, "/")`
	bslash := len(b) >= 1 && b[:1] == "/"        //@comment : was `strings.HasPrefix(b, "/")`
	switch {
	case aslash && bslash:
		return a + b[1:]
	case !aslash && !bslash:
		return a + "/" + b
	}
	return a + b
}

// NewSingleHostReverseProxy returns a new ReverseProxy that routes
// URLs to the scheme, host, and base path provided in target. If the
// target's path is "/base" and the incoming request was for "/dir",
// the target request will be for /base/dir.
// NewSingleHostReverseProxy does not rewrite the Host header.
// To rewrite Host headers, use ReverseProxy directly with a custom
// Director policy.
//noinspection GoUnusedExportedFunction
func NewSingleHostReverseProxy(target *url.URL) *ReverseProxy {
	targetQuery := target.RawQuery
	director := func(req *Request) {
		req.URL.Scheme = target.Scheme
		req.URL.Host = target.Host
		req.URL.Path = singleJoiningSlash(target.Path, req.URL.Path)
		if targetQuery == "" || req.URL.RawQuery == "" {
			req.URL.RawQuery = targetQuery + req.URL.RawQuery
		} else {
			req.URL.RawQuery = targetQuery + "&" + req.URL.RawQuery
		}
		if _, ok := req.Header[hdr.UserAgent]; !ok {
			// explicitly disable User-Agent so it's not set to default value
			req.Header.Set(hdr.UserAgent, "")
		}
	}
	return &ReverseProxy{Director: director}
}
