/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package tests

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"fmt"
	"go/ast"
	"io"
	"io/ioutil"
	"net/url"
	"reflect"
	"regexp"
	"strings"
	"testing"

	. "github.com/badu/http"
)

type respTest struct {
	Raw  string
	Resp Response
	Body string
}

var respTests = []respTest{
	// Unchunked response without Content-Length.
	{
		"HTTP/1.0 200 OK\r\n" +
			"Connection: close\r\n" +
			"\r\n" +
			"Body here\n",

		Response{
			Status:     "200 OK",
			StatusCode: 200,
			Proto:      "HTTP/1.0",
			ProtoMajor: 1,
			ProtoMinor: 0,
			Request:    dummyReq(GET),
			Header: Header{
				Connection: {DoClose}, // TODO(rsc): Delete?
			},
			Close:         true,
			ContentLength: -1,
		},

		"Body here\n",
	},

	// Unchunked HTTP/1.1 response without Content-Length or
	// Connection headers.
	{
		"HTTP/1.1 200 OK\r\n" +
			"\r\n" +
			"Body here\n",

		Response{
			Status:        "200 OK",
			StatusCode:    200,
			Proto:         HTTP1_1,
			ProtoMajor:    1,
			ProtoMinor:    1,
			Header:        Header{},
			Request:       dummyReq(GET),
			Close:         true,
			ContentLength: -1,
		},

		"Body here\n",
	},

	// Unchunked HTTP/1.1 204 response without Content-Length.
	{
		"HTTP/1.1 204 No Content\r\n" +
			"\r\n" +
			"Body should not be read!\n",

		Response{
			Status:        "204 No Content",
			StatusCode:    204,
			Proto:         HTTP1_1,
			ProtoMajor:    1,
			ProtoMinor:    1,
			Header:        Header{},
			Request:       dummyReq(GET),
			Close:         false,
			ContentLength: 0,
		},

		"",
	},

	// Unchunked response with Content-Length.
	{
		"HTTP/1.0 200 OK\r\n" +
			"Content-Length: 10\r\n" +
			"Connection: close\r\n" +
			"\r\n" +
			"Body here\n",

		Response{
			Status:     "200 OK",
			StatusCode: 200,
			Proto:      "HTTP/1.0",
			ProtoMajor: 1,
			ProtoMinor: 0,
			Request:    dummyReq(GET),
			Header: Header{
				Connection:    {DoClose},
				ContentLength: {"10"},
			},
			Close:         true,
			ContentLength: 10,
		},

		"Body here\n",
	},

	// Chunked response without Content-Length.
	{
		"HTTP/1.1 200 OK\r\n" +
			"Transfer-Encoding: chunked\r\n" +
			"\r\n" +
			"0a\r\n" +
			"Body here\n\r\n" +
			"09\r\n" +
			"continued\r\n" +
			"0\r\n" +
			"\r\n",

		Response{
			Status:           "200 OK",
			StatusCode:       200,
			Proto:            HTTP1_1,
			ProtoMajor:       1,
			ProtoMinor:       1,
			Request:          dummyReq(GET),
			Header:           Header{},
			Close:            false,
			ContentLength:    -1,
			TransferEncoding: []string{DoChunked},
		},

		"Body here\ncontinued",
	},

	// Chunked response with Content-Length.
	{
		"HTTP/1.1 200 OK\r\n" +
			"Transfer-Encoding: chunked\r\n" +
			"Content-Length: 10\r\n" +
			"\r\n" +
			"0a\r\n" +
			"Body here\n\r\n" +
			"0\r\n" +
			"\r\n",

		Response{
			Status:           "200 OK",
			StatusCode:       200,
			Proto:            HTTP1_1,
			ProtoMajor:       1,
			ProtoMinor:       1,
			Request:          dummyReq(GET),
			Header:           Header{},
			Close:            false,
			ContentLength:    -1,
			TransferEncoding: []string{DoChunked},
		},

		"Body here\n",
	},

	// Chunked response in response to a HEAD request
	{
		"HTTP/1.1 200 OK\r\n" +
			"Transfer-Encoding: chunked\r\n" +
			"\r\n",

		Response{
			Status:           "200 OK",
			StatusCode:       200,
			Proto:            HTTP1_1,
			ProtoMajor:       1,
			ProtoMinor:       1,
			Request:          dummyReq("HEAD"),
			Header:           Header{},
			TransferEncoding: []string{DoChunked},
			Close:            false,
			ContentLength:    -1,
		},

		"",
	},

	// Content-Length in response to a HEAD request
	{
		"HTTP/1.0 200 OK\r\n" +
			"Content-Length: 256\r\n" +
			"\r\n",

		Response{
			Status:           "200 OK",
			StatusCode:       200,
			Proto:            "HTTP/1.0",
			ProtoMajor:       1,
			ProtoMinor:       0,
			Request:          dummyReq("HEAD"),
			Header:           Header{ContentLength: {"256"}},
			TransferEncoding: nil,
			Close:            true,
			ContentLength:    256,
		},

		"",
	},

	// Content-Length in response to a HEAD request with HTTP/1.1
	{
		"HTTP/1.1 200 OK\r\n" +
			"Content-Length: 256\r\n" +
			"\r\n",

		Response{
			Status:           "200 OK",
			StatusCode:       200,
			Proto:            HTTP1_1,
			ProtoMajor:       1,
			ProtoMinor:       1,
			Request:          dummyReq("HEAD"),
			Header:           Header{ContentLength: {"256"}},
			TransferEncoding: nil,
			Close:            false,
			ContentLength:    256,
		},

		"",
	},

	// No Content-Length or Chunked in response to a HEAD request
	{
		"HTTP/1.0 200 OK\r\n" +
			"\r\n",

		Response{
			Status:           "200 OK",
			StatusCode:       200,
			Proto:            "HTTP/1.0",
			ProtoMajor:       1,
			ProtoMinor:       0,
			Request:          dummyReq("HEAD"),
			Header:           Header{},
			TransferEncoding: nil,
			Close:            true,
			ContentLength:    -1,
		},

		"",
	},

	// explicit Content-Length of 0.
	{
		"HTTP/1.1 200 OK\r\n" +
			"Content-Length: 0\r\n" +
			"\r\n",

		Response{
			Status:     "200 OK",
			StatusCode: 200,
			Proto:      HTTP1_1,
			ProtoMajor: 1,
			ProtoMinor: 1,
			Request:    dummyReq(GET),
			Header: Header{
				ContentLength: {"0"},
			},
			Close:         false,
			ContentLength: 0,
		},

		"",
	},

	// Status line without a Reason-Phrase, but trailing space.
	// (permitted by RFC 2616)
	{
		"HTTP/1.0 303 \r\n\r\n",
		Response{
			Status:        "303 ",
			StatusCode:    303,
			Proto:         "HTTP/1.0",
			ProtoMajor:    1,
			ProtoMinor:    0,
			Request:       dummyReq(GET),
			Header:        Header{},
			Close:         true,
			ContentLength: -1,
		},

		"",
	},

	// Status line without a Reason-Phrase, and no trailing space.
	// (not permitted by RFC 2616, but we'll accept it anyway)
	{
		"HTTP/1.0 303\r\n\r\n",
		Response{
			Status:        "303",
			StatusCode:    303,
			Proto:         "HTTP/1.0",
			ProtoMajor:    1,
			ProtoMinor:    0,
			Request:       dummyReq(GET),
			Header:        Header{},
			Close:         true,
			ContentLength: -1,
		},

		"",
	},

	// golang.org/issue/4767: don't special-case multipart/byteranges responses
	{
		`HTTP/1.1 206 Partial Content
Connection: close
Content-Type: multipart/byteranges; boundary=18a75608c8f47cef

some body`,
		Response{
			Status:     "206 Partial Content",
			StatusCode: 206,
			Proto:      HTTP1_1,
			ProtoMajor: 1,
			ProtoMinor: 1,
			Request:    dummyReq(GET),
			Header: Header{
				ContentType: []string{"multipart/byteranges; boundary=18a75608c8f47cef"},
			},
			Close:         true,
			ContentLength: -1,
		},

		"some body",
	},

	// Unchunked response without Content-Length, Request is nil
	{
		"HTTP/1.0 200 OK\r\n" +
			"Connection: close\r\n" +
			"\r\n" +
			"Body here\n",

		Response{
			Status:     "200 OK",
			StatusCode: 200,
			Proto:      "HTTP/1.0",
			ProtoMajor: 1,
			ProtoMinor: 0,
			Header: Header{
				Connection: {DoClose}, // TODO(rsc): Delete?
			},
			Close:         true,
			ContentLength: -1,
		},

		"Body here\n",
	},

	// 206 Partial Content. golang.org/issue/8923
	{
		"HTTP/1.1 206 Partial Content\r\n" +
			"Content-Type: text/plain; charset=utf-8\r\n" +
			"Accept-Ranges: bytes\r\n" +
			"Content-Range: bytes 0-5/1862\r\n" +
			"Content-Length: 6\r\n\r\n" +
			"foobar",

		Response{
			Status:     "206 Partial Content",
			StatusCode: 206,
			Proto:      HTTP1_1,
			ProtoMajor: 1,
			ProtoMinor: 1,
			Request:    dummyReq(GET),
			Header: Header{
				AcceptRanges:  []string{"bytes"},
				ContentLength: []string{"6"},
				ContentType:   []string{"text/plain; charset=utf-8"},
				ContentRange:  []string{"bytes 0-5/1862"},
			},
			ContentLength: 6,
		},

		"foobar",
	},

	// Both keep-alive and close, on the same Connection line. (Issue 8840)
	{
		"HTTP/1.1 200 OK\r\n" +
			"Content-Length: 256\r\n" +
			"Connection: keep-alive, close\r\n" +
			"\r\n",

		Response{
			Status:     "200 OK",
			StatusCode: 200,
			Proto:      HTTP1_1,
			ProtoMajor: 1,
			ProtoMinor: 1,
			Request:    dummyReq("HEAD"),
			Header: Header{
				ContentLength: {"256"},
			},
			TransferEncoding: nil,
			Close:            true,
			ContentLength:    256,
		},

		"",
	},

	// Both keep-alive and close, on different Connection lines. (Issue 8840)
	{
		"HTTP/1.1 200 OK\r\n" +
			"Content-Length: 256\r\n" +
			"Connection: keep-alive\r\n" +
			"Connection: close\r\n" +
			"\r\n",

		Response{
			Status:     "200 OK",
			StatusCode: 200,
			Proto:      HTTP1_1,
			ProtoMajor: 1,
			ProtoMinor: 1,
			Request:    dummyReq("HEAD"),
			Header: Header{
				ContentLength: {"256"},
			},
			TransferEncoding: nil,
			Close:            true,
			ContentLength:    256,
		},

		"",
	},

	// Issue 12785: HTTP/1.0 response with bogus (to be ignored) Transfer-Encoding.
	// Without a Content-Length.
	{
		"HTTP/1.0 200 OK\r\n" +
			"Transfer-Encoding: bogus\r\n" +
			"\r\n" +
			"Body here\n",

		Response{
			Status:        "200 OK",
			StatusCode:    200,
			Proto:         "HTTP/1.0",
			ProtoMajor:    1,
			ProtoMinor:    0,
			Request:       dummyReq(GET),
			Header:        Header{},
			Close:         true,
			ContentLength: -1,
		},

		"Body here\n",
	},

	// Issue 12785: HTTP/1.0 response with bogus (to be ignored) Transfer-Encoding.
	// With a Content-Length.
	{
		"HTTP/1.0 200 OK\r\n" +
			"Transfer-Encoding: bogus\r\n" +
			"Content-Length: 10\r\n" +
			"\r\n" +
			"Body here\n",

		Response{
			Status:     "200 OK",
			StatusCode: 200,
			Proto:      "HTTP/1.0",
			ProtoMajor: 1,
			ProtoMinor: 0,
			Request:    dummyReq(GET),
			Header: Header{
				ContentLength: {"10"},
			},
			Close:         true,
			ContentLength: 10,
		},

		"Body here\n",
	},

	{
		"HTTP/1.1 200 OK\r\n" +
			"Content-Encoding: gzip\r\n" +
			"Content-Length: 23\r\n" +
			"Connection: keep-alive\r\n" +
			"Keep-Alive: timeout=7200\r\n\r\n" +
			"\x1f\x8b\b\x00\x00\x00\x00\x00\x00\x00s\xf3\xf7\a\x00\xab'\xd4\x1a\x03\x00\x00\x00",
		Response{
			Status:     "200 OK",
			StatusCode: 200,
			Proto:      HTTP1_1,
			ProtoMajor: 1,
			ProtoMinor: 1,
			Request:    dummyReq(GET),
			Header: Header{
				ContentLength:   {"23"},
				ContentEncoding: {"gzip"},
				Connection:      {DoKeepAlive},
				KeepAlive:       {"timeout=7200"},
			},
			Close:         false,
			ContentLength: 23,
		},
		"\x1f\x8b\b\x00\x00\x00\x00\x00\x00\x00s\xf3\xf7\a\x00\xab'\xd4\x1a\x03\x00\x00\x00",
	},

	// Issue 19989: two spaces between HTTP version and status.
	{
		"HTTP/1.0  401 Unauthorized\r\n" +
			"Content-type: text/html\r\n" +
			"WWW-Authenticate: Basic realm=\"\"\r\n\r\n" +
			"Your Authentication failed.\r\n",
		Response{
			Status:     "401 Unauthorized",
			StatusCode: 401,
			Proto:      "HTTP/1.0",
			ProtoMajor: 1,
			ProtoMinor: 0,
			Request:    dummyReq(GET),
			Header: Header{
				ContentType:        {"text/html"},
				"Www-Authenticate": {`Basic realm=""`},
			},
			Close:         true,
			ContentLength: -1,
		},
		"Your Authentication failed.\r\n",
	},
}

type responseLocationTest struct {
	location string // Response's Location header or ""
	requrl   string // Response.Request.URL or ""
	want     string
	wantErr  error
}

var responseLocationTests = []responseLocationTest{
	{"/foo", "http://bar.com/baz", "http://bar.com/foo", nil},
	{"http://foo.com/", "http://bar.com/baz", "http://foo.com/", nil},
	{"", "http://bar.com/baz", "", ErrNoLocation},
	{"/bar", "", "/bar", nil},
}

func dummyReq(method string) *Request {
	return &Request{Method: method}
}

func dummyReq11(method string) *Request {
	return &Request{Method: method, Proto: HTTP1_1, ProtoMajor: 1, ProtoMinor: 1}
}

// tests successful calls to ReadResponse, and inspects the returned Response.
// For error cases, see TestReadResponseErrors below.
func TestReadResponse(t *testing.T) {
	for i, tt := range respTests {
		resp, err := ReadResponse(bufio.NewReader(strings.NewReader(tt.Raw)), tt.Resp.Request)
		if err != nil {
			t.Errorf("#%d: %v", i, err)
			continue
		}
		rbody := resp.Body
		resp.Body = nil
		diff(t, fmt.Sprintf("#%d Response", i), resp, &tt.Resp)
		var bout bytes.Buffer
		if rbody != nil {
			_, err = io.Copy(&bout, rbody)
			if err != nil {
				t.Errorf("#%d: %v", i, err)
				continue
			}
			rbody.Close()
		}
		body := bout.String()
		if body != tt.Body {
			t.Errorf("#%d: Body = %q want %q", i, body, tt.Body)
		}
	}
}

func TestWriteResponse(t *testing.T) {
	for i, tt := range respTests {
		resp, err := ReadResponse(bufio.NewReader(strings.NewReader(tt.Raw)), tt.Resp.Request)
		if err != nil {
			t.Errorf("#%d: %v", i, err)
			continue
		}
		err = resp.Write(ioutil.Discard)
		if err != nil {
			t.Errorf("#%d: %v", i, err)
			continue
		}
	}
}

// TestReadResponseCloseInMiddle tests that closing a body after
// reading only part of its contents advances the read to the end of
// the request, right up until the next request.
func TestReadResponseCloseInMiddle(t *testing.T) {
	var readResponseCloseInMiddleTests = []struct {
		chunked, compressed bool
	}{
		{false, false},
		{true, false},
		{true, true},
	}

	t.Parallel()
	for _, test := range readResponseCloseInMiddleTests {
		fatalf := func(format string, args ...interface{}) {
			args = append([]interface{}{test.chunked, test.compressed}, args...)
			t.Fatalf("on test chunked=%v, compressed=%v: "+format, args...)
		}
		checkErr := func(err error, msg string) {
			if err == nil {
				return
			}
			fatalf(msg+": %v", err)
		}
		var buf bytes.Buffer
		buf.WriteString("HTTP/1.1 200 OK\r\n")
		if test.chunked {
			buf.WriteString("Transfer-Encoding: chunked\r\n")
		} else {
			buf.WriteString("Content-Length: 1000000\r\n")
		}
		var wr io.Writer = &buf
		if test.chunked {
			wr = NewChunkedWriter(wr)
		}
		if test.compressed {
			buf.WriteString("Content-Encoding: gzip\r\n")
			wr = gzip.NewWriter(wr)
		}
		buf.WriteString("\r\n")

		chunk := bytes.Repeat([]byte{'x'}, 1000)
		for i := 0; i < 1000; i++ {
			if test.compressed {
				// Otherwise this compresses too well.
				_, err := io.ReadFull(rand.Reader, chunk)
				checkErr(err, "rand.Reader ReadFull")
			}
			wr.Write(chunk)
		}
		if test.compressed {
			err := wr.(*gzip.Writer).Close()
			checkErr(err, "compressor close")
		}
		if test.chunked {
			buf.WriteString("0\r\n\r\n")
		}
		buf.WriteString("Next Request Here")

		bufr := bufio.NewReader(&buf)
		resp, err := ReadResponse(bufr, dummyReq(GET))
		checkErr(err, "ReadResponse")
		expectedLength := int64(-1)
		if !test.chunked {
			expectedLength = 1000000
		}
		if resp.ContentLength != expectedLength {
			fatalf("expected response length %d, got %d", expectedLength, resp.ContentLength)
		}
		if resp.Body == nil {
			fatalf("nil body")
		}
		if test.compressed {
			gzReader, err := gzip.NewReader(resp.Body)
			checkErr(err, "gzip.NewHeaderReader")
			resp.Body = &readerAndCloser{gzReader, resp.Body}
		}

		rbuf := make([]byte, 2500)
		n, err := io.ReadFull(resp.Body, rbuf)
		checkErr(err, "2500 byte ReadFull")
		if n != 2500 {
			fatalf("ReadFull only read %d bytes", n)
		}
		if test.compressed == false && !bytes.Equal(bytes.Repeat([]byte{'x'}, 2500), rbuf) {
			fatalf("ReadFull didn't read 2500 'x'; got %q", string(rbuf))
		}
		resp.CloseBody()

		rest, err := ioutil.ReadAll(bufr)
		checkErr(err, "ReadAll on remainder")
		if e, g := "Next Request Here", string(rest); e != g {
			g = regexp.MustCompile(`(xx+)`).ReplaceAllStringFunc(g, func(match string) string {
				return fmt.Sprintf("x(repeated x%d)", len(match))
			})
			fatalf("remainder = %q, expected %q", g, e)
		}
	}
}

func diff(t *testing.T, prefix string, have, want interface{}) {
	hv := reflect.ValueOf(have).Elem()
	wv := reflect.ValueOf(want).Elem()
	if hv.Type() != wv.Type() {
		t.Errorf("%s: type mismatch %v want %v", prefix, hv.Type(), wv.Type())
	}
	for i := 0; i < hv.NumField(); i++ {
		name := hv.Type().Field(i).Name
		if !ast.IsExported(name) {
			continue
		}
		hf := hv.Field(i).Interface()
		wf := wv.Field(i).Interface()
		if !reflect.DeepEqual(hf, wf) {
			t.Errorf("%s: %s = %v want %v", prefix, name, hf, wf)
		}
	}
}

func TestLocationResponse(t *testing.T) {
	for i, tt := range responseLocationTests {
		res := new(Response)
		res.Header = make(Header)
		res.Header.Set(Location, tt.location)
		if tt.requrl != "" {
			res.Request = &Request{}
			var err error
			res.Request.URL, err = url.Parse(tt.requrl)
			if err != nil {
				t.Fatalf("bad test URL %q: %v", tt.requrl, err)
			}
		}

		got, err := res.Location()
		if tt.wantErr != nil {
			if err == nil {
				t.Errorf("%d. err=nil; want %q", i, tt.wantErr)
				continue
			}
			if g, e := err.Error(), tt.wantErr.Error(); g != e {
				t.Errorf("%d. err=%q; want %q", i, g, e)
				continue
			}
			continue
		}
		if err != nil {
			t.Errorf("%d. err=%q", i, err)
			continue
		}
		if g, e := got.String(), tt.want; g != e {
			t.Errorf("%d. Location=%q; want %q", i, g, e)
		}
	}
}

func TestResponseStatusStutter(t *testing.T) {
	r := &Response{
		Status:     "123 some status",
		StatusCode: 123,
		ProtoMajor: 1,
		ProtoMinor: 3,
	}
	var buf bytes.Buffer
	r.Write(&buf)
	if strings.Contains(buf.String(), "123 123") {
		t.Errorf("stutter in status: %s", buf.String())
	}
}

func TestResponseContentLengthShortBody(t *testing.T) {
	const shortBody = "Short body, not 123 bytes."
	br := bufio.NewReader(strings.NewReader("HTTP/1.1 200 OK\r\n" +
		"Content-Length: 123\r\n" +
		"\r\n" +
		shortBody))
	res, err := ReadResponse(br, &Request{Method: GET})
	if err != nil {
		t.Fatal(err)
	}
	if res.ContentLength != 123 {
		t.Fatalf("Content-Length = %d; want 123", res.ContentLength)
	}
	var buf bytes.Buffer
	n, err := io.Copy(&buf, res.Body)
	if n != int64(len(shortBody)) {
		t.Errorf("Copied %d bytes; want %d, len(%q)", n, len(shortBody), shortBody)
	}
	if buf.String() != shortBody {
		t.Errorf("Read body %q; want %q", buf.String(), shortBody)
	}
	if err != io.ErrUnexpectedEOF {
		t.Errorf("io.Copy error = %#v; want io.ErrUnexpectedEOF", err)
	}
}

// Test various ReadResponse error cases. (also tests success cases, but mostly
// it's about errors).  This does not test anything involving the bodies. Only
// the return value from ReadResponse itself.
func TestReadResponseErrors(t *testing.T) {
	type testCase struct {
		name    string // optional, defaults to in
		in      string
		header  Header
		wantErr interface{} // nil, err value, or string substring
	}

	status := func(s string, wantErr interface{}) testCase {
		if wantErr == true {
			wantErr = "malformed HTTP status code"
		}
		return testCase{
			name:    fmt.Sprintf("status %q", s),
			in:      "HTTP/1.1 " + s + "\r\nFoo: bar\r\n\r\n",
			wantErr: wantErr,
		}
	}

	version := func(s string, wantErr interface{}) testCase {
		if wantErr == true {
			wantErr = "malformed HTTP version"
		}
		return testCase{
			name:    fmt.Sprintf("version %q", s),
			in:      s + " 200 OK\r\n\r\n",
			wantErr: wantErr,
		}
	}

	contentLength := func(status, body string, wantErr interface{}, header Header) testCase {
		return testCase{
			name:    fmt.Sprintf("status %q %q", status, body),
			in:      fmt.Sprintf("HTTP/1.1 %s\r\n%s", status, body),
			wantErr: wantErr,
			header:  header,
		}
	}

	errMultiCL := "message cannot contain multiple Content-Length headers"

	tests := []testCase{
		{"", "", nil, io.ErrUnexpectedEOF},
		{"", "HTTP/1.1 301 Moved Permanently\r\nFoo: bar", nil, io.ErrUnexpectedEOF},
		{"", HTTP1_1, nil, "malformed HTTP response"},
		status("20X Unknown", true),
		status("abcd Unknown", true),
		status("二百/两百 OK", true),
		status(" Unknown", true),
		status("c8 OK", true),
		status("0x12d Moved Permanently", true),
		status("200 OK", nil),
		status("000 OK", nil),
		status("001 OK", nil),
		status("404 NOTFOUND", nil),
		status("20 OK", true),
		status("00 OK", true),
		status("-10 OK", true),
		status("1000 OK", true),
		status("999 Done", nil),
		status("-1 OK", true),
		status("-200 OK", true),
		version("HTTP/1.2", nil),
		version("HTTP/2.0", nil),
		version("HTTP/1.100000000002", true),
		version("HTTP/1.-1", true),
		version("HTTP/A.B", true),
		version("HTTP/1", true),
		version("http/1.1", true),

		contentLength("200 OK", "Content-Length: 10\r\nContent-Length: 7\r\n\r\nGopher hey\r\n", errMultiCL, nil),
		contentLength("200 OK", "Content-Length: 7\r\nContent-Length: 7\r\n\r\nGophers\r\n", nil, Header{ContentLength: {"7"}}),
		contentLength("201 OK", "Content-Length: 0\r\nContent-Length: 7\r\n\r\nGophers\r\n", errMultiCL, nil),
		contentLength("300 OK", "Content-Length: 0\r\nContent-Length: 0 \r\n\r\nGophers\r\n", nil, Header{ContentLength: {"0"}}),
		contentLength("200 OK", "Content-Length:\r\nContent-Length:\r\n\r\nGophers\r\n", nil, nil),
		contentLength("206 OK", "Content-Length:\r\nContent-Length: 0 \r\nConnection: close\r\n\r\nGophers\r\n", errMultiCL, nil),

		// multiple content-length headers for 204 and 304 should still be checked
		contentLength("204 OK", "Content-Length: 7\r\nContent-Length: 8\r\n\r\n", errMultiCL, nil),
		contentLength("204 OK", "Content-Length: 3\r\nContent-Length: 3\r\n\r\n", nil, nil),
		contentLength("304 OK", "Content-Length: 880\r\nContent-Length: 1\r\n\r\n", errMultiCL, nil),
		contentLength("304 OK", "Content-Length: 961\r\nContent-Length: 961\r\n\r\n", nil, nil),
	}

	for i, tt := range tests {
		br := bufio.NewReader(strings.NewReader(tt.in))
		_, rerr := ReadResponse(br, nil)
		if err := matchErr(rerr, tt.wantErr); err != nil {
			name := tt.name
			if name == "" {
				name = fmt.Sprintf("%d. input %q", i, tt.in)
			}
			t.Errorf("%s: %v", name, err)
		}
	}
}

// wantErr can be nil, an error value to match exactly, or type string to
// match a substring.
func matchErr(err error, wantErr interface{}) error {
	if err == nil {
		if wantErr == nil {
			return nil
		}
		if sub, ok := wantErr.(string); ok {
			return fmt.Errorf("unexpected success; want error with substring %q", sub)
		}
		return fmt.Errorf("unexpected success; want error %v", wantErr)
	}
	if wantErr == nil {
		return fmt.Errorf("%v; want success", err)
	}
	if sub, ok := wantErr.(string); ok {
		if strings.Contains(err.Error(), sub) {
			return nil
		}
		return fmt.Errorf("error = %v; want an error with substring %q", err, sub)
	}
	if err == wantErr {
		return nil
	}
	return fmt.Errorf("%v; want %v", err, wantErr)
}

// A response should only write out single Connection: close header. Tests #19499.
func TestResponseWritesOnlySingleConnectionClose(t *testing.T) {
	const connectionCloseHeader = "Connection: close"

	res, err := ReadResponse(bufio.NewReader(strings.NewReader("HTTP/1.0 200 OK\r\n\r\nAAAA")), nil)
	if err != nil {
		t.Fatalf("ReadResponse failed %v", err)
	}

	var buf1 bytes.Buffer
	if err = res.Write(&buf1); err != nil {
		t.Fatalf("Write failed %v", err)
	}
	if res, err = ReadResponse(bufio.NewReader(&buf1), nil); err != nil {
		t.Fatalf("ReadResponse failed %v", err)
	}

	var buf2 bytes.Buffer
	if err = res.Write(&buf2); err != nil {
		t.Fatalf("Write failed %v", err)
	}
	if count := strings.Count(buf2.String(), connectionCloseHeader); count != 1 {
		t.Errorf("Found %d %q header", count, connectionCloseHeader)
	}
}
func TestResponseWrite(t *testing.T) {

	type respWriteTest struct {
		Resp Response
		Raw  string
	}

	respWriteTests := []respWriteTest{
		// HTTP/1.0, identity coding; no trailer
		{
			Response{
				StatusCode:    503,
				ProtoMajor:    1,
				ProtoMinor:    0,
				Request:       dummyReq(GET),
				Header:        Header{},
				Body:          ioutil.NopCloser(strings.NewReader("abcdef")),
				ContentLength: 6,
			},

			"HTTP/1.0 503 Service Unavailable\r\n" +
				"Content-Length: 6\r\n\r\n" +
				"abcdef",
		},
		// Unchunked response without Content-Length.
		{
			Response{
				StatusCode:    200,
				ProtoMajor:    1,
				ProtoMinor:    0,
				Request:       dummyReq(GET),
				Header:        Header{},
				Body:          ioutil.NopCloser(strings.NewReader("abcdef")),
				ContentLength: -1,
			},
			"HTTP/1.0 200 OK\r\n" +
				"\r\n" +
				"abcdef",
		},
		// HTTP/1.1 response with unknown length and Connection: close
		{
			Response{
				StatusCode:    200,
				ProtoMajor:    1,
				ProtoMinor:    1,
				Request:       dummyReq(GET),
				Header:        Header{},
				Body:          ioutil.NopCloser(strings.NewReader("abcdef")),
				ContentLength: -1,
				Close:         true,
			},
			"HTTP/1.1 200 OK\r\n" +
				"Connection: close\r\n" +
				"\r\n" +
				"abcdef",
		},
		// HTTP/1.1 response with unknown length and not setting connection: close
		{
			Response{
				StatusCode:    200,
				ProtoMajor:    1,
				ProtoMinor:    1,
				Request:       dummyReq11(GET),
				Header:        Header{},
				Body:          ioutil.NopCloser(strings.NewReader("abcdef")),
				ContentLength: -1,
				Close:         false,
			},
			"HTTP/1.1 200 OK\r\n" +
				"Connection: close\r\n" +
				"\r\n" +
				"abcdef",
		},
		// HTTP/1.1 response with unknown length and not setting connection: close, but
		// setting chunked.
		{
			Response{
				StatusCode:       200,
				ProtoMajor:       1,
				ProtoMinor:       1,
				Request:          dummyReq11(GET),
				Header:           Header{},
				Body:             ioutil.NopCloser(strings.NewReader("abcdef")),
				ContentLength:    -1,
				TransferEncoding: []string{DoChunked},
				Close:            false,
			},
			"HTTP/1.1 200 OK\r\n" +
				"Transfer-Encoding: chunked\r\n\r\n" +
				"6\r\nabcdef\r\n0\r\n\r\n",
		},
		// HTTP/1.1 response 0 content-length, and nil body
		{
			Response{
				StatusCode:    200,
				ProtoMajor:    1,
				ProtoMinor:    1,
				Request:       dummyReq11(GET),
				Header:        Header{},
				Body:          nil,
				ContentLength: 0,
				Close:         false,
			},
			"HTTP/1.1 200 OK\r\n" +
				"Content-Length: 0\r\n" +
				"\r\n",
		},
		// HTTP/1.1 response 0 content-length, and non-nil empty body
		{
			Response{
				StatusCode:    200,
				ProtoMajor:    1,
				ProtoMinor:    1,
				Request:       dummyReq11(GET),
				Header:        Header{},
				Body:          ioutil.NopCloser(strings.NewReader("")),
				ContentLength: 0,
				Close:         false,
			},
			"HTTP/1.1 200 OK\r\n" +
				"Content-Length: 0\r\n" +
				"\r\n",
		},
		// HTTP/1.1 response 0 content-length, and non-nil non-empty body
		{
			Response{
				StatusCode:    200,
				ProtoMajor:    1,
				ProtoMinor:    1,
				Request:       dummyReq11(GET),
				Header:        Header{},
				Body:          ioutil.NopCloser(strings.NewReader("foo")),
				ContentLength: 0,
				Close:         false,
			},
			"HTTP/1.1 200 OK\r\n" +
				"Connection: close\r\n" +
				"\r\nfoo",
		},
		// HTTP/1.1, chunked coding; empty trailer; close
		{
			Response{
				StatusCode:       200,
				ProtoMajor:       1,
				ProtoMinor:       1,
				Request:          dummyReq(GET),
				Header:           Header{},
				Body:             ioutil.NopCloser(strings.NewReader("abcdef")),
				ContentLength:    6,
				TransferEncoding: []string{DoChunked},
				Close:            true,
			},

			"HTTP/1.1 200 OK\r\n" +
				"Connection: close\r\n" +
				"Transfer-Encoding: chunked\r\n\r\n" +
				"6\r\nabcdef\r\n0\r\n\r\n",
		},

		// Header value with a newline character (Issue 914).
		// Also tests removal of leading and trailing whitespace.
		{
			Response{
				StatusCode: 204,
				ProtoMajor: 1,
				ProtoMinor: 1,
				Request:    dummyReq(GET),
				Header: Header{
					"Foo": []string{" Bar\nBaz "},
				},
				Body:             nil,
				ContentLength:    0,
				TransferEncoding: []string{DoChunked},
				Close:            true,
			},

			"HTTP/1.1 204 No Content\r\n" +
				"Connection: close\r\n" +
				"Foo: Bar Baz\r\n" +
				"\r\n",
		},

		// Want a single Content-Length header. Fixing issue 8180 where
		// there were two.
		{
			Response{
				StatusCode:       StatusOK,
				ProtoMajor:       1,
				ProtoMinor:       1,
				Request:          &Request{Method: POST},
				Header:           Header{},
				ContentLength:    0,
				TransferEncoding: nil,
				Body:             nil,
			},
			"HTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\n",
		},

		// When a response to a POST has Content-Length: -1, make sure we don't
		// write the Content-Length as -1.
		{
			Response{
				StatusCode:    StatusOK,
				ProtoMajor:    1,
				ProtoMinor:    1,
				Request:       &Request{Method: POST},
				Header:        Header{},
				ContentLength: -1,
				Body:          ioutil.NopCloser(strings.NewReader("abcdef")),
			},
			"HTTP/1.1 200 OK\r\nConnection: close\r\n\r\nabcdef",
		},

		// Status code under 100 should be zero-padded to
		// three digits.  Still bogus, but less bogus. (be
		// consistent with generating three digits, since the
		// Transport requires it)
		{
			Response{
				StatusCode: 7,
				Status:     "license to violate specs",
				ProtoMajor: 1,
				ProtoMinor: 0,
				Request:    dummyReq(GET),
				Header:     Header{},
				Body:       nil,
			},

			"HTTP/1.0 007 license to violate specs\r\nContent-Length: 0\r\n\r\n",
		},

		// No stutter.  Status code in 1xx range response should
		// not include a Content-Length header.  See issue #16942.
		{
			Response{
				StatusCode: 123,
				Status:     "123 Sesame Street",
				ProtoMajor: 1,
				ProtoMinor: 0,
				Request:    dummyReq(GET),
				Header:     Header{},
				Body:       nil,
			},

			"HTTP/1.0 123 Sesame Street\r\n\r\n",
		},

		// Status code 204 (No content) response should not include a
		// Content-Length header.  See issue #16942.
		{
			Response{
				StatusCode: 204,
				Status:     "No Content",
				ProtoMajor: 1,
				ProtoMinor: 0,
				Request:    dummyReq(GET),
				Header:     Header{},
				Body:       nil,
			},

			"HTTP/1.0 204 No Content\r\n\r\n",
		},
	}

	for i := range respWriteTests {
		tt := &respWriteTests[i]
		var braw bytes.Buffer
		err := tt.Resp.Write(&braw)
		if err != nil {
			t.Errorf("error writing #%d: %s", i, err)
			continue
		}
		sraw := braw.String()
		if sraw != tt.Raw {
			t.Errorf("Test %d, expecting:\n%q\nGot:\n%q\n", i, tt.Raw, sraw)
			continue
		}
	}
}

func TestReadRequest(t *testing.T) {
	var (
		noError          = ""
		noTrailer Header = nil
		noBodyStr        = ""
	)
	type reqTest struct {
		Raw     string
		Req     *Request
		Body    string
		Trailer Header
		Error   string
	}

	var reqTests = []reqTest{
		// Baseline test; All Request fields included for template use
		{
			"GET http://www.techcrunch.com/ HTTP/1.1\r\n" +
				"Host: www.techcrunch.com\r\n" +
				"User-Agent: Fake\r\n" +
				"Accept: text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8\r\n" +
				"Accept-Language: en-us,en;q=0.5\r\n" +
				"Accept-Encoding: gzip,deflate\r\n" +
				"Accept-Charset: ISO-8859-1,utf-8;q=0.7,*;q=0.7\r\n" +
				"Keep-Alive: 300\r\n" +
				"Content-Length: 7\r\n" +
				"Proxy-Connection: keep-alive\r\n\r\n" +
				"abcdef\n???",

			&Request{
				Method: GET,
				URL: &url.URL{
					Scheme: HTTP,
					Host:   "www.techcrunch.com",
					Path:   "/",
				},
				Proto:      HTTP1_1,
				ProtoMajor: 1,
				ProtoMinor: 1,
				Header: Header{
					Accept:          {"text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8"},
					AcceptLanguage:  {"en-us,en;q=0.5"},
					AcceptEncoding:  {"gzip,deflate"},
					AcceptCharset:   {"ISO-8859-1,utf-8;q=0.7,*;q=0.7"},
					KeepAlive:       {"300"},
					ProxyConnection: {DoKeepAlive},
					ContentLength:   {"7"},
					UserAgent:       {"Fake"},
				},
				Close:         false,
				ContentLength: 7,
				Host:          "www.techcrunch.com",
				RequestURI:    "http://www.techcrunch.com/",
			},

			"abcdef\n",

			noTrailer,
			noError,
		},

		// GET request with no body (the normal case)
		{
			"GET / HTTP/1.1\r\n" +
				"Host: foo.com\r\n\r\n",

			&Request{
				Method: GET,
				URL: &url.URL{
					Path: "/",
				},
				Proto:         HTTP1_1,
				ProtoMajor:    1,
				ProtoMinor:    1,
				Header:        Header{},
				Close:         false,
				ContentLength: 0,
				Host:          "foo.com",
				RequestURI:    "/",
			},

			noBodyStr,
			noTrailer,
			noError,
		},

		// Tests that we don't parse a path that looks like a
		// scheme-relative URI as a scheme-relative URI.
		{
			"GET //user@host/is/actually/a/path/ HTTP/1.1\r\n" +
				"Host: test\r\n\r\n",

			&Request{
				Method: GET,
				URL: &url.URL{
					Path: "//user@host/is/actually/a/path/",
				},
				Proto:         HTTP1_1,
				ProtoMajor:    1,
				ProtoMinor:    1,
				Header:        Header{},
				Close:         false,
				ContentLength: 0,
				Host:          "test",
				RequestURI:    "//user@host/is/actually/a/path/",
			},

			noBodyStr,
			noTrailer,
			noError,
		},

		// Tests a bogus abs_path on the Request-Line (RFC 2616 section 5.1.2)
		{
			"GET ../../../../etc/passwd HTTP/1.1\r\n" +
				"Host: test\r\n\r\n",
			nil,
			noBodyStr,
			noTrailer,
			"parse ../../../../etc/passwd: invalid URI for request",
		},

		// Tests missing URL:
		{
			"GET  HTTP/1.1\r\n" +
				"Host: test\r\n\r\n",
			nil,
			noBodyStr,
			noTrailer,
			"parse : empty url",
		},

		// Tests chunked body with trailer:
		{
			"POST / HTTP/1.1\r\n" +
				"Host: foo.com\r\n" +
				"Transfer-Encoding: chunked\r\n\r\n" +
				"3\r\nfoo\r\n" +
				"3\r\nbar\r\n" +
				"0\r\n" +
				"Trailer-Key: Trailer-Value\r\n" +
				"\r\n",
			&Request{
				Method: POST,
				URL: &url.URL{
					Path: "/",
				},
				TransferEncoding: []string{DoChunked},
				Proto:            HTTP1_1,
				ProtoMajor:       1,
				ProtoMinor:       1,
				Header:           Header{},
				ContentLength:    -1,
				Host:             "foo.com",
				RequestURI:       "/",
			},

			"foobar",
			Header{
				"Trailer-Key": {"Trailer-Value"},
			},
			noError,
		},

		// Tests chunked body and a bogus Content-Length which should be deleted.
		{
			"POST / HTTP/1.1\r\n" +
				"Host: foo.com\r\n" +
				"Transfer-Encoding: chunked\r\n" +
				"Content-Length: 9999\r\n\r\n" + // to be removed.
				"3\r\nfoo\r\n" +
				"3\r\nbar\r\n" +
				"0\r\n" +
				"\r\n",
			&Request{
				Method: POST,
				URL: &url.URL{
					Path: "/",
				},
				TransferEncoding: []string{DoChunked},
				Proto:            HTTP1_1,
				ProtoMajor:       1,
				ProtoMinor:       1,
				Header:           Header{},
				ContentLength:    -1,
				Host:             "foo.com",
				RequestURI:       "/",
			},

			"foobar",
			noTrailer,
			noError,
		},

		// CONNECT request with domain name:
		{
			"CONNECT www.google.com:443 HTTP/1.1\r\n\r\n",

			&Request{
				Method: CONNECT,
				URL: &url.URL{
					Host: "www.google.com:443",
				},
				Proto:         HTTP1_1,
				ProtoMajor:    1,
				ProtoMinor:    1,
				Header:        Header{},
				Close:         false,
				ContentLength: 0,
				Host:          "www.google.com:443",
				RequestURI:    "www.google.com:443",
			},

			noBodyStr,
			noTrailer,
			noError,
		},

		// CONNECT request with IP address:
		{
			"CONNECT 127.0.0.1:6060 HTTP/1.1\r\n\r\n",

			&Request{
				Method: CONNECT,
				URL: &url.URL{
					Host: "127.0.0.1:6060",
				},
				Proto:         HTTP1_1,
				ProtoMajor:    1,
				ProtoMinor:    1,
				Header:        Header{},
				Close:         false,
				ContentLength: 0,
				Host:          "127.0.0.1:6060",
				RequestURI:    "127.0.0.1:6060",
			},

			noBodyStr,
			noTrailer,
			noError,
		},

		// CONNECT request for RPC:
		{
			"CONNECT /_goRPC_ HTTP/1.1\r\n\r\n",

			&Request{
				Method: CONNECT,
				URL: &url.URL{
					Path: "/_goRPC_",
				},
				Proto:         HTTP1_1,
				ProtoMajor:    1,
				ProtoMinor:    1,
				Header:        Header{},
				Close:         false,
				ContentLength: 0,
				Host:          "",
				RequestURI:    "/_goRPC_",
			},

			noBodyStr,
			noTrailer,
			noError,
		},

		// SSDP Notify request. golang.org/issue/3692
		{
			"NOTIFY * HTTP/1.1\r\nServer: foo\r\n\r\n",
			&Request{
				Method: "NOTIFY",
				URL: &url.URL{
					Path: "*",
				},
				Proto:      HTTP1_1,
				ProtoMajor: 1,
				ProtoMinor: 1,
				Header: Header{
					ServerHeader: []string{"foo"},
				},
				Close:         false,
				ContentLength: 0,
				RequestURI:    "*",
			},

			noBodyStr,
			noTrailer,
			noError,
		},

		// OPTIONS request. Similar to golang.org/issue/3692
		{
			"OPTIONS * HTTP/1.1\r\nServer: foo\r\n\r\n",
			&Request{
				Method: OPTIONS,
				URL: &url.URL{
					Path: "*",
				},
				Proto:      HTTP1_1,
				ProtoMajor: 1,
				ProtoMinor: 1,
				Header: Header{
					ServerHeader: []string{"foo"},
				},
				Close:         false,
				ContentLength: 0,
				RequestURI:    "*",
			},

			noBodyStr,
			noTrailer,
			noError,
		},

		// Connection: close. golang.org/issue/8261
		{
			"GET / HTTP/1.1\r\nHost: issue8261.com\r\nConnection: close\r\n\r\n",
			&Request{
				Method: GET,
				URL: &url.URL{
					Path: "/",
				},
				Header: Header{
					// This wasn't removed from Go 1.0 to
					// Go 1.3, so locking it in that we
					// keep this:
					Connection: []string{DoClose},
				},
				Host:       "issue8261.com",
				Proto:      HTTP1_1,
				ProtoMajor: 1,
				ProtoMinor: 1,
				Close:      true,
				RequestURI: "/",
			},

			noBodyStr,
			noTrailer,
			noError,
		},

		// HEAD with Content-Length 0. Make sure this is permitted,
		// since I think we used to send it.
		{
			"HEAD / HTTP/1.1\r\nHost: issue8261.com\r\nConnection: close\r\nContent-Length: 0\r\n\r\n",
			&Request{
				Method: HEAD,
				URL: &url.URL{
					Path: "/",
				},
				Header: Header{
					Connection:    []string{DoClose},
					ContentLength: []string{"0"},
				},
				Host:       "issue8261.com",
				Proto:      HTTP1_1,
				ProtoMajor: 1,
				ProtoMinor: 1,
				Close:      true,
				RequestURI: "/",
			},

			noBodyStr,
			noTrailer,
			noError,
		},
	}
	for i := range reqTests {
		tt := &reqTests[i]
		req, err := ReadRequest(bufio.NewReader(strings.NewReader(tt.Raw)))
		if err != nil {
			if err.Error() != tt.Error {
				t.Errorf("#%d: error %q, want error %q", i, err.Error(), tt.Error)
			}
			continue
		}
		rbody := req.Body
		req.Body = nil
		testName := fmt.Sprintf("Test %d (%q)", i, tt.Raw)
		diff(t, testName, req, tt.Req)
		var bout bytes.Buffer
		if rbody != nil {
			_, err := io.Copy(&bout, rbody)
			if err != nil {
				t.Fatalf("%s: copying body: %v", testName, err)
			}
			rbody.Close()
		}
		body := bout.String()
		if body != tt.Body {
			t.Errorf("%s: Body = %q want %q", testName, body, tt.Body)
		}
		if !reflect.DeepEqual(tt.Trailer, req.Trailer) {
			t.Errorf("%s: Trailers differ.\n got: %v\nwant: %v", testName, req.Trailer, tt.Trailer)
		}
	}
}

func TestReadRequestBad(t *testing.T) {
	var badRequestTests = []struct {
		name string
		req  []byte
	}{
		{"bad_connect_host", reqBytes("CONNECT []%20%48%54%54%50%2f%31%2e%31%0a%4d%79%48%65%61%64%65%72%3a%20%31%32%33%0a%0a HTTP/1.0")},
		{"smuggle_two_contentlen", reqBytes(`POST / HTTP/1.1
Content-Length: 3
Content-Length: 4

abc`)},
		{"smuggle_content_len_head", reqBytes(`HEAD / HTTP/1.1
Host: foo
Content-Length: 5`)},
	}

	for _, tt := range badRequestTests {
		got, err := ReadRequest(bufio.NewReader(bytes.NewReader(tt.req)))
		if err == nil {
			all, err := ioutil.ReadAll(got.Body)
			t.Errorf("%s: got unexpected request = %#v\n  Body = %q, %v", tt.name, got, all, err)
		}
	}
}
