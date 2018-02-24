/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package tests

import (
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net"
	"net/url"
	"os"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	. "http"
	"http/cli"
	"http/th"
	. "http/tport"
	"http/util"
)

// Testing the newClientServerTest helper itself.
func TestNewClientServerTest(t *testing.T) {
	var got struct {
		sync.Mutex
		log []string
	}
	h := HandlerFunc(func(w ResponseWriter, r *Request) {
		got.Lock()
		defer got.Unlock()
		got.log = append(got.log, r.Proto)
	})

	cst := newClientServerTest(t, h)
	if _, err := cst.c.Head(cst.ts.URL); err != nil {
		t.Fatal(err)
	}
	cst.close()

	got.Lock() // no need to unlock
	if want := []string{HTTP1_1}; !reflect.DeepEqual(got.log, want) {
		t.Errorf("got %q; want %q", got.log, want)
	}
}

func TestChunkedResponseHeaders(t *testing.T) {
	defer afterTest(t)
	log.SetOutput(ioutil.Discard) // is noisy otherwise
	defer log.SetOutput(os.Stderr)
	cst := newClientServerTest(t, HandlerFunc(func(w ResponseWriter, r *Request) {
		w.Header().Set(ContentLength, "intentional gibberish") // we check that this is deleted
		w.(Flusher).Flush()
		fmt.Fprintf(w, "I am a chunked response.")
	}))
	defer cst.close()

	res, err := cst.c.Get(cst.ts.URL)
	if err != nil {
		t.Fatalf("Get error: %v", err)
	}
	defer res.CloseBody()
	if g, e := res.ContentLength, int64(-1); g != e {
		t.Errorf("expected ContentLength of %d; got %d", e, g)
	}
	wantTE := []string{DoChunked}

	if !reflect.DeepEqual(res.TransferEncoding, wantTE) {
		t.Errorf("TransferEncoding = %v; want %v", res.TransferEncoding, wantTE)
	}
	if got, haveCL := res.Header[ContentLength]; haveCL {
		t.Errorf("Unexpected Content-Length: %q", got)
	}
}

// Issue 13532
func TestHeadContentLengthNoBody(t *testing.T) {
	runWrapper{
		ReqFunc: (*cli.Client).Head,
		Handler: func(w ResponseWriter, r *Request) {
		},
	}.run(t)
}

func TestHeadContentLengthSmallBody(t *testing.T) {
	runWrapper{
		ReqFunc: (*cli.Client).Head,
		Handler: func(w ResponseWriter, r *Request) {
			io.WriteString(w, "small")
		},
	}.run(t)
}

func TestHeadContentLengthLargeBody(t *testing.T) {
	runWrapper{
		ReqFunc: (*cli.Client).Head,
		Handler: func(w ResponseWriter, r *Request) {
			chunk := strings.Repeat("x", 512<<10)
			for i := 0; i < 10; i++ {
				io.WriteString(w, chunk)
			}
		},
	}.run(t)
}

func Test200NoBody(t *testing.T) {
	runWrapper{Handler: func(w ResponseWriter, r *Request) {}}.run(t)
}

func Test204NoBody(t *testing.T) { testnoBody(t, 204) }

func Test304NoBody(t *testing.T) { testnoBody(t, 304) }

func Test404NoBody(t *testing.T) { testnoBody(t, 404) }

func testnoBody(t *testing.T, status int) {
	runWrapper{Handler: func(w ResponseWriter, r *Request) {
		w.WriteHeader(status)
	}}.run(t)
}

func TestSmallBody(t *testing.T) {
	runWrapper{Handler: func(w ResponseWriter, r *Request) {
		io.WriteString(w, "small body")
	}}.run(t)
}

func TestExplicitContentLength(t *testing.T) {
	runWrapper{Handler: func(w ResponseWriter, r *Request) {
		w.Header().Set(ContentLength, "3")
		io.WriteString(w, "foo")
	}}.run(t)
}

func TestFlushBeforeBody(t *testing.T) {
	runWrapper{Handler: func(w ResponseWriter, r *Request) {
		w.(Flusher).Flush()
		io.WriteString(w, "foo")
	}}.run(t)
}

func TestFlushMidBody(t *testing.T) {
	runWrapper{Handler: func(w ResponseWriter, r *Request) {
		io.WriteString(w, "foo")
		w.(Flusher).Flush()
		io.WriteString(w, "bar")
	}}.run(t)
}

func TestHead_ExplicitLen(t *testing.T) {
	runWrapper{
		ReqFunc: (*cli.Client).Head,
		Handler: func(w ResponseWriter, r *Request) {
			if r.Method != HEAD {
				t.Errorf("unexpected method %q", r.Method)
			}
			w.Header().Set(ContentLength, "1235")
		},
	}.run(t)
}

func TestHead_ImplicitLen(t *testing.T) {
	runWrapper{
		ReqFunc: (*cli.Client).Head,
		Handler: func(w ResponseWriter, r *Request) {
			if r.Method != HEAD {
				t.Errorf("unexpected method %q", r.Method)
			}
			io.WriteString(w, "foo")
		},
	}.run(t)
}

func TestHandlerWritesTooLittle(t *testing.T) {
	runWrapper{
		Handler: func(w ResponseWriter, r *Request) {
			w.Header().Set(ContentLength, "3")
			io.WriteString(w, "12") // one byte short
		},
		CheckResponse: func(res *Response) {
			sr, ok := res.Body.(slurpResult)
			if !ok {
				t.Errorf("%s body is %T; want slurpResult", HTTP1_1, res.Body)
				return
			}
			if sr.err != io.ErrUnexpectedEOF {
				t.Errorf("%s read error = %v; want io.ErrUnexpectedEOF", HTTP1_1, sr.err)
			}
			if string(sr.body) != "12" {
				t.Errorf("%s body = %q; want %q", HTTP1_1, sr.body, "12")
			}
		},
	}.run(t)
}

// Tests that the HTTP/1 and HTTP/2 servers prevent handlers from
// writing more than they declared. This test does not test whether
// the transport deals with too much data, though, since the server
// doesn't make it possible to send bogus data. For those tests, see
// transport_test.go (for HTTP/1) or x/net/http2/transport_test.go
// (for HTTP/2).
func TestHandlerWritesTooMuch(t *testing.T) {
	runWrapper{
		Handler: func(w ResponseWriter, r *Request) {
			w.Header().Set(ContentLength, "3")
			w.(Flusher).Flush()
			io.WriteString(w, "123")
			w.(Flusher).Flush()
			n, err := io.WriteString(w, "x") // too many
			if n > 0 || err == nil {
				t.Errorf("for proto %q, final write = %v, %v; want 0, some error", r.Proto, n, err)
			}
		},
	}.run(t)
}

// Verify that both our HTTP/1 and HTTP/2 request and auto-decompress gzip.
// Some hosts send gzip even if you don't ask for it; see golang.org/issue/13298
func TestAutoGzip(t *testing.T) {
	runWrapper{
		Handler: func(w ResponseWriter, r *Request) {
			if ae := r.Header.Get(AcceptEncoding); ae != "gzip" {
				t.Errorf("%s Accept-Encoding = %q; want gzip", r.Proto, ae)
			}
			w.Header().Set(ContentEncoding, "gzip")
			gz := gzip.NewWriter(w)
			io.WriteString(gz, "I am some gzipped content. Go go go go go go go go go go go go should compress well.")
			gz.Close()
		},
	}.run(t)
}

func TestAutoGzipDisabled(t *testing.T) {
	runWrapper{
		Opts: []interface{}{
			func(tr *Transport) { tr.DisableCompression = true },
		},
		Handler: func(w ResponseWriter, r *Request) {
			fmt.Fprintf(w, "%q", r.Header[AcceptEncoding])
			if ae := r.Header.Get(AcceptEncoding); ae != "" {
				t.Errorf("%s Accept-Encoding = %q; want empty", r.Proto, ae)
			}
		},
	}.run(t)
}

// Test304Responses verifies that 304s don't declare that they're
// chunking in their response headers and aren't allowed to produce
// output.
func Test304Responses(t *testing.T) {
	defer afterTest(t)
	cst := newClientServerTest(t, HandlerFunc(func(w ResponseWriter, r *Request) {
		w.WriteHeader(StatusNotModified)
		_, err := w.Write([]byte("illegal body"))
		if err != ErrBodyNotAllowed {
			t.Errorf("on Write, expected ErrBodyNotAllowed, got %v", err)
		}
	}))
	defer cst.close()
	res, err := cst.c.Get(cst.ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.TransferEncoding) > 0 {
		t.Errorf("expected no TransferEncoding; got %v", res.TransferEncoding)
	}
	body, err := ioutil.ReadAll(res.Body)
	if err != nil {
		t.Error(err)
	}
	if len(body) > 0 {
		t.Errorf("got unexpected body %q", string(body))
	}
}

func TestServerEmptyContentLength(t *testing.T) {
	runWrapper{
		Handler: func(w ResponseWriter, r *Request) {
			w.Header()[ContentType] = []string{""}
			io.WriteString(w, "<html><body>hi</body></html>")
		},
	}.run(t)
}

func TestRequestContentLengthKnownNonZero(t *testing.T) {
	testRequestContentLength(t, func() io.Reader { return strings.NewReader("FOUR") }, 4)
}

func TestRequestContentLengthKnownZero(t *testing.T) {
	testRequestContentLength(t, func() io.Reader { return nil }, 0)
}

func TestRequestContentLengthUnknown(t *testing.T) {
	testRequestContentLength(t, func() io.Reader { return struct{ io.Reader }{strings.NewReader("Stuff")} }, -1)
}

func testRequestContentLength(t *testing.T, bodyfn func() io.Reader, wantLen int64) {
	runWrapper{
		Handler: func(w ResponseWriter, r *Request) {
			w.Header().Set("Got-Length", fmt.Sprint(r.ContentLength))
			fmt.Fprintf(w, "Req.ContentLength=%v", r.ContentLength)
		},
		ReqFunc: func(c *cli.Client, url string) (*Response, error) {
			return c.Post(url, "text/plain", bodyfn())
		},
		CheckResponse: func(res *Response) {
			if got, want := res.Header.Get("Got-Length"), fmt.Sprint(wantLen); got != want {
				t.Errorf("Proto %q got length %q; want %q", HTTP1_1, got, want)
			}
		},
	}.run(t)
}

// Tests that clients can send trailers to a server and that the server can read them.
func TestTrailersClientToServer(t *testing.T) {
	defer afterTest(t)
	cst := newClientServerTest(t, HandlerFunc(func(w ResponseWriter, r *Request) {
		var decl []string
		for k := range r.Trailer {
			decl = append(decl, k)
		}
		sort.Strings(decl)

		slurp, err := ioutil.ReadAll(r.Body)
		if err != nil {
			t.Errorf("Server reading request body: %v", err)
		}
		if string(slurp) != "foo" {
			t.Errorf("Server read request body %q; want foo", slurp)
		}
		if r.Trailer == nil {
			io.WriteString(w, "nil Trailer")
		} else {
			fmt.Fprintf(w, "decl: %v, vals: %s, %s",
				decl,
				r.Trailer.Get("Client-Trailer-A"),
				r.Trailer.Get("Client-Trailer-B"))
		}
	}))
	defer cst.close()

	var req *Request
	req, _ = NewRequest(POST, cst.ts.URL, io.MultiReader(
		eofReaderFunc(func() {
			req.Trailer["Client-Trailer-A"] = []string{"valuea"}
		}),
		strings.NewReader("foo"),
		eofReaderFunc(func() {
			req.Trailer["Client-Trailer-B"] = []string{"valueb"}
		}),
	))
	req.Trailer = Header{
		"Client-Trailer-A": nil, //  to be set later
		"Client-Trailer-B": nil, //  to be set later
	}
	req.ContentLength = -1
	res, err := cst.c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if err := wantBody(res, err, "decl: [Client-Trailer-A Client-Trailer-B], vals: valuea, valueb"); err != nil {
		t.Error(err)
	}
}

// Tests that servers send trailers to a client and that the client can read them.
func TestTrailersServerToClient(t *testing.T) { testTrailersServerToClient(t, false) }

func TestTrailersServerToClientFlush(t *testing.T) { testTrailersServerToClient(t, true) }

func testTrailersServerToClient(t *testing.T, flush bool) {
	defer afterTest(t)
	const body = "Some body"
	cst := newClientServerTest(t, HandlerFunc(func(w ResponseWriter, r *Request) {
		w.Header().Set(Trailer, "Server-Trailer-A, Server-Trailer-B")
		w.Header().Add(Trailer, "Server-Trailer-C")

		io.WriteString(w, body)
		if flush {
			w.(Flusher).Flush()
		}

		// How handlers set Trailers: declare it ahead of time
		// with the Trailer header, and then mutate the
		// Header() of those values later, after the response
		// has been written (we wrote to w above).
		w.Header().Set("Server-Trailer-A", "valuea")
		w.Header().Set("Server-Trailer-C", "valuec") // skipping B
		w.Header().Set("Server-Trailer-NotDeclared", "should be omitted")
	}))
	defer cst.close()

	res, err := cst.c.Get(cst.ts.URL)
	if err != nil {
		t.Fatal(err)
	}

	wantHeader := Header{
		ContentType: {"text/plain; charset=utf-8"},
	}
	wantLen := -1

	if res.ContentLength != int64(wantLen) {
		t.Errorf("ContentLength = %v; want %v", res.ContentLength, wantLen)
	}

	delete(res.Header, Date) // irrelevant for test
	if !reflect.DeepEqual(res.Header, wantHeader) {
		t.Errorf("Header = %v; want %v", res.Header, wantHeader)
	}

	if got, want := res.Trailer, (Header{
		"Server-Trailer-A": nil,
		"Server-Trailer-B": nil,
		"Server-Trailer-C": nil,
	}); !reflect.DeepEqual(got, want) {
		t.Errorf("Trailer before body read = %v; want %v", got, want)
	}

	if err := wantBody(res, nil, body); err != nil {
		t.Fatal(err)
	}

	if got, want := res.Trailer, (Header{
		"Server-Trailer-A": {"valuea"},
		"Server-Trailer-B": nil,
		"Server-Trailer-C": {"valuec"},
	}); !reflect.DeepEqual(got, want) {
		t.Errorf("Trailer after body read = %v; want %v", got, want)
	}
}

// Don't allow a Body.Read after Body.Close. Issue 13648.
func TestResponseBodyReadAfterClose(t *testing.T) {
	defer afterTest(t)
	const body = "Some body"
	cst := newClientServerTest(t, HandlerFunc(func(w ResponseWriter, r *Request) {
		io.WriteString(w, body)
	}))
	defer cst.close()
	res, err := cst.c.Get(cst.ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	res.CloseBody()
	data, err := ioutil.ReadAll(res.Body)
	if len(data) != 0 || err == nil {
		t.Fatalf("ReadAll returned %q, %v; want error", data, err)
	}
}

func TestConcurrentReadWriteReqBody(t *testing.T) {
	defer afterTest(t)
	const reqBody = "some request body"
	const resBody = "some response body"
	cst := newClientServerTest(t, HandlerFunc(func(w ResponseWriter, r *Request) {
		var wg sync.WaitGroup
		wg.Add(2)
		didRead := make(chan bool, 1)
		// Read in one goroutine.
		go func() {
			defer wg.Done()
			data, err := ioutil.ReadAll(r.Body)
			if string(data) != reqBody {
				t.Errorf("Handler read %q; want %q", data, reqBody)
			}
			if err != nil {
				t.Errorf("Handler Read: %v", err)
			}
			didRead <- true
		}()
		// Write in another goroutine.
		go func() {
			defer wg.Done()
			// our HTTP/1 implementation intentionally
			// doesn't permit writes during read (mostly
			// due to it being undefined); if that is ever
			// relaxed, change this.
			<-didRead

			io.WriteString(w, resBody)
		}()
		wg.Wait()
	}))
	defer cst.close()
	req, _ := NewRequest(POST, cst.ts.URL, strings.NewReader(reqBody))
	req.Header.Add(Expect, "100-continue") // just to complicate things
	res, err := cst.c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	data, err := ioutil.ReadAll(res.Body)
	defer res.CloseBody()
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != resBody {
		t.Errorf("read %q; want %q", data, resBody)
	}
}

func TestConnectRequest(t *testing.T) {
	defer afterTest(t)
	gotc := make(chan *Request, 1)
	cst := newClientServerTest(t, HandlerFunc(func(w ResponseWriter, r *Request) {
		gotc <- r
	}))
	defer cst.close()

	u, err := url.Parse(cst.ts.URL)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		req  *Request
		want string
	}{
		{
			req: &Request{
				Method: CONNECT,
				Header: Header{},
				URL:    u,
			},
			want: u.Host,
		},
		{
			req: &Request{
				Method: CONNECT,
				Header: Header{},
				URL:    u,
				Host:   "example.com:123",
			},
			want: "example.com:123",
		},
	}

	for i, tt := range tests {
		res, err := cst.c.Do(tt.req)
		if err != nil {
			t.Errorf("%d. RoundTrip = %v", i, err)
			continue
		}
		res.CloseBody()
		req := <-gotc
		if req.Method != CONNECT {
			t.Errorf("method = %q; want CONNECT", req.Method)
		}
		if req.Host != tt.want {
			t.Errorf("Host = %q; want %q", req.Host, tt.want)
		}
		if req.URL.Host != tt.want {
			t.Errorf("URL.Host = %q; want %q", req.URL.Host, tt.want)
		}
	}
}

func TestTransportUserAgent(t *testing.T) {
	defer afterTest(t)
	cst := newClientServerTest(t, HandlerFunc(func(w ResponseWriter, r *Request) {
		fmt.Fprintf(w, "%q", r.Header[UserAgent])
	}))
	defer cst.close()

	either := func(a, b string) string {
		return a
	}

	tests := []struct {
		setup func(*Request)
		want  string
	}{
		{
			func(r *Request) {},
			either(`["Go-http-client/1.1"]`, `["Go-http-client/2.0"]`),
		},
		{
			func(r *Request) { r.Header.Set(UserAgent, "foo/1.2.3") },
			`["foo/1.2.3"]`,
		},
		{
			func(r *Request) { r.Header[UserAgent] = []string{"single", "or", "multiple"} },
			`["single"]`,
		},
		{
			func(r *Request) { r.Header.Set(UserAgent, "") },
			`[]`,
		},
		{
			func(r *Request) { r.Header[UserAgent] = nil },
			`[]`,
		},
	}
	for i, tt := range tests {
		req, _ := NewRequest(GET, cst.ts.URL, nil)
		tt.setup(req)
		res, err := cst.c.Do(req)
		if err != nil {
			t.Errorf("%d. RoundTrip = %v", i, err)
			continue
		}
		slurp, err := ioutil.ReadAll(res.Body)
		res.CloseBody()
		if err != nil {
			t.Errorf("%d. read body = %v", i, err)
			continue
		}
		if string(slurp) != tt.want {
			t.Errorf("%d. body mismatch.\n got: %s\nwant: %s\n", i, slurp, tt.want)
		}
	}
}

func TestStarRequestFoo(t *testing.T) { testStarRequest(t, "FOO") }

func TestStarRequestOptions(t *testing.T) { testStarRequest(t, OPTIONS) }

func testStarRequest(t *testing.T, method string) {
	defer afterTest(t)
	gotc := make(chan *Request, 1)
	cst := newClientServerTest(t, HandlerFunc(func(w ResponseWriter, r *Request) {
		w.Header().Set("foo", "bar")
		gotc <- r
		w.(Flusher).Flush()
	}))
	defer cst.close()

	u, err := url.Parse(cst.ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "*"

	req := &Request{
		Method: method,
		Header: Header{},
		URL:    u,
	}

	res, err := cst.c.Do(req)
	if err != nil {
		t.Fatalf("RoundTrip = %v", err)
	}
	res.CloseBody()

	wantFoo := "bar"
	wantLen := int64(-1)
	if method == OPTIONS {
		wantFoo = ""
		wantLen = 0
	}
	if res.StatusCode != 200 {
		t.Errorf("status code = %v; want %d", res.Status, 200)
	}
	if res.ContentLength != wantLen {
		t.Errorf("content length = %v; want %d", res.ContentLength, wantLen)
	}
	if got := res.Header.Get("foo"); got != wantFoo {
		t.Errorf("response \"foo\" header = %q; want %q", got, wantFoo)
	}
	select {
	case req = <-gotc:
	default:
		req = nil
	}
	if req == nil {
		if method != OPTIONS {
			t.Fatalf("handler never got request")
		}
		return
	}
	if req.Method != method {
		t.Errorf("method = %q; want %q", req.Method, method)
	}
	if req.URL.Path != "*" {
		t.Errorf("URL.Path = %q; want *", req.URL.Path)
	}
	if req.RequestURI != "*" {
		t.Errorf("RequestURI = %q; want *", req.RequestURI)
	}
}

// tests that Transport doesn't retain a pointer to the provided request.
func TestTransportGCRequestBody(t *testing.T) { testTransportGCRequest(t, true) }

func TestTransportGCRequestNoBody(t *testing.T) { testTransportGCRequest(t, false) }

func testTransportGCRequest(t *testing.T, body bool) {
	setParallel(t)
	defer afterTest(t)
	cst := newClientServerTest(t, HandlerFunc(func(w ResponseWriter, r *Request) {
		ioutil.ReadAll(r.Body)
		if body {
			io.WriteString(w, "Hello.")
		}
	}))
	defer cst.close()

	didGC := make(chan struct{})
	(func() {
		body := strings.NewReader("some body")
		req, _ := NewRequest(POST, cst.ts.URL, body)
		runtime.SetFinalizer(req, func(*Request) { close(didGC) })
		res, err := cst.c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ioutil.ReadAll(res.Body); err != nil {
			t.Fatal(err)
		}
		if err := res.Body.Close(); err != nil {
			t.Fatal(err)
		}
	})()
	timeout := time.NewTimer(5 * time.Second)
	defer timeout.Stop()
	for {
		select {
		case <-didGC:
			return
		case <-time.After(100 * time.Millisecond):
			runtime.GC()
		case <-timeout.C:
			t.Fatal("never saw GC of request")
		}
	}
}

func TestTransportRejectsInvalidHeaders(t *testing.T) {
	setParallel(t)
	defer afterTest(t)
	cst := newClientServerTest(t, HandlerFunc(func(w ResponseWriter, r *Request) {
		fmt.Fprintf(w, "Handler saw headers: %q", r.Header)
	}), optQuietLog)
	defer cst.close()
	cst.tr.DisableKeepAlives = true

	tests := []struct {
		key, val string
		ok       bool
	}{
		{"Foo", "capital-key", true}, // verify h2 allows capital keys
		{"Foo", "foo\x00bar", false}, // \x00 byte in value not allowed
		{"Foo", "two\nlines", false}, // \n byte in value not allowed
		{"bogus\nkey", "v", false},   // \n byte also not allowed in key
		{"A space", "v", false},      // spaces in keys not allowed
		{"имя", "v", false},          // key must be ascii
		{"name", "валю", true},       // value may be non-ascii
		{"", "v", false},             // key must be non-empty
		{"k", "", true},              // value may be empty
	}
	for _, tt := range tests {
		dialedc := make(chan bool, 1)
		cst.tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			dialedc <- true
			return net.Dial(network, addr)
		}
		req, _ := NewRequest(GET, cst.ts.URL, nil)
		req.Header[tt.key] = []string{tt.val}
		res, err := cst.c.Do(req)
		var body []byte
		if err == nil {
			body, _ = ioutil.ReadAll(res.Body)
			res.CloseBody()
		}
		var dialed bool
		select {
		case <-dialedc:
			dialed = true
		default:
		}

		if !tt.ok && dialed {
			t.Errorf("For key %q, value %q, transport dialed. Expected local failure. Response was: (%v, %v)\nServer replied with: %s", tt.key, tt.val, res, err, body)
		} else if (err == nil) != tt.ok {
			t.Errorf("For key %q, value %q; got err = %v; want ok=%v", tt.key, tt.val, err, tt.ok)
		}
	}
}

// Tests that we support bogus under-100 HTTP statuses, because we historically
// have. This might change at some point, but not yet in Go 1.6.
func TestBogusStatusWorks(t *testing.T) {
	defer afterTest(t)
	const code = 7
	cst := newClientServerTest(t, HandlerFunc(func(w ResponseWriter, r *Request) {
		w.WriteHeader(code)
	}))
	defer cst.close()

	res, err := cst.c.Get(cst.ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != code {
		t.Errorf("StatusCode = %d; want %d", res.StatusCode, code)
	}
}

func TestInterruptWithPanic(t *testing.T) { testInterruptWithPanic(t, "boom") }

func TestInterruptWithPanicNil(t *testing.T) { testInterruptWithPanic(t, nil) }

func TestInterruptWithPanicErrAbortHandler(t *testing.T) {
	testInterruptWithPanic(t, ErrAbortHandler)
}

func testInterruptWithPanic(t *testing.T, panicValue interface{}) {
	setParallel(t)
	const msg = "hello"
	defer afterTest(t)

	testDone := make(chan struct{})
	defer close(testDone)

	var errorLog lockedBytesBuffer
	gotHeaders := make(chan bool, 1)
	cst := newClientServerTest(t, HandlerFunc(func(w ResponseWriter, r *Request) {
		io.WriteString(w, msg)
		w.(Flusher).Flush()

		select {
		case <-gotHeaders:
		case <-testDone:
		}
		panic(panicValue)
	}), func(ts *th.TestServer) {
		ts.Server.ErrorLog = log.New(&errorLog, "", 0)
	})
	defer cst.close()
	res, err := cst.c.Get(cst.ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	gotHeaders <- true
	defer res.CloseBody()
	slurp, err := ioutil.ReadAll(res.Body)
	if string(slurp) != msg {
		t.Errorf("client read %q; want %q", slurp, msg)
	}
	if err == nil {
		t.Errorf("client read all successfully; want some error")
	}
	logOutput := func() string {
		errorLog.Lock()
		defer errorLog.Unlock()
		return errorLog.String()
	}
	wantStackLogged := panicValue != nil && panicValue != ErrAbortHandler

	if err := waitErrCondition(5*time.Second, 10*time.Millisecond, func() error {
		gotLog := logOutput()
		if !wantStackLogged {
			if gotLog == "" {
				return nil
			}
			return fmt.Errorf("want no log output; got: %s", gotLog)
		}
		if gotLog == "" {
			return fmt.Errorf("wanted a stack trace logged; got nothing")
		}
		if !strings.Contains(gotLog, "created by ") && strings.Count(gotLog, "\n") < 6 {
			return fmt.Errorf("output doesn't look like a panic stack trace. Got: %s", gotLog)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// Issue 15366
func TestAutoGzipWithDumpResponse(t *testing.T) {
	runWrapper{
		Handler: func(w ResponseWriter, r *Request) {
			h := w.Header()
			h.Set(ContentEncoding, "gzip")
			h.Set(ContentLength, "23")
			h.Set(Connection, DoKeepAlive)
			io.WriteString(w, "\x1f\x8b\b\x00\x00\x00\x00\x00\x00\x00s\xf3\xf7\a\x00\xab'\xd4\x1a\x03\x00\x00\x00")
		},
		EarlyCheckResponse: func(res *Response) {
			if !res.Uncompressed {
				t.Errorf("%s: expected Uncompressed to be set", HTTP1_1)
			}
			dump, err := util.DumpResponse(res, true)
			if err != nil {
				t.Errorf("%s: DumpResponse: %v", HTTP1_1, err)
				return
			}
			if strings.Contains(string(dump), "Connection: close") {
				t.Errorf("%s: should not see \"Connection: close\" in dump; got:\n%s", HTTP1_1, dump)
			}
			if !strings.Contains(string(dump), "FOO") {
				t.Errorf("%s: should see \"FOO\" in response; got:\n%s", HTTP1_1, dump)
			}
		},
	}.run(t)
}

// Issue 14607
func TestCloseIdleConnections(t *testing.T) {
	setParallel(t)
	defer afterTest(t)
	cst := newClientServerTest(t, HandlerFunc(func(w ResponseWriter, r *Request) {
		w.Header().Set("X-Addr", r.RemoteAddr)
	}))
	defer cst.close()
	get := func() string {
		res, err := cst.c.Get(cst.ts.URL)
		if err != nil {
			t.Fatal(err)
		}
		res.CloseBody()
		v := res.Header.Get("X-Addr")
		if v == "" {
			t.Fatal("didn't get X-Addr")
		}
		return v
	}
	a1 := get()
	cst.tr.CloseIdleConnections()
	a2 := get()
	if a1 == a2 {
		t.Errorf("didn't close connection")
	}
}

func TestNoSniffExpectRequestBody(t *testing.T) {
	defer afterTest(t)
	cst := newClientServerTest(t, HandlerFunc(func(w ResponseWriter, r *Request) {
		w.WriteHeader(StatusUnauthorized)
	}))
	defer cst.close()

	// Set ExpectContinueTimeout non-zero so RoundTrip won't try to write it.
	cst.tr.ExpectContinueTimeout = 10 * time.Second

	req, err := NewRequest(POST, cst.ts.URL, testErrorReader{t})
	if err != nil {
		t.Fatal(err)
	}
	req.ContentLength = 0 // so transport is tempted to sniff it
	req.Header.Set(Expect, "100-continue")
	res, err := cst.tr.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.CloseBody()
	if res.StatusCode != StatusUnauthorized {
		t.Errorf("status code = %v; want %v", res.StatusCode, StatusUnauthorized)
	}
}

func TestServerUndeclaredTrailers(t *testing.T) {
	defer afterTest(t)
	cst := newClientServerTest(t, HandlerFunc(func(w ResponseWriter, r *Request) {
		w.Header().Set("Foo", "Bar")
		w.Header().Set("Trailer:Foo", "Baz")
		w.(Flusher).Flush()
		w.Header().Add("Trailer:Foo", "Baz2")
		w.Header().Set("Trailer:Bar", "Quux")
	}))
	defer cst.close()
	res, err := cst.c.Get(cst.ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(ioutil.Discard, res.Body); err != nil {
		t.Fatal(err)
	}
	res.CloseBody()
	delete(res.Header, Date)
	delete(res.Header, ContentType)

	if want := (Header{"Foo": {"Bar"}}); !reflect.DeepEqual(res.Header, want) {
		t.Errorf("Header = %#v; want %#v", res.Header, want)
	}
	if want := (Header{"Foo": {"Baz", "Baz2"}, "Bar": {"Quux"}}); !reflect.DeepEqual(res.Trailer, want) {
		t.Errorf("Trailer = %#v; want %#v", res.Trailer, want)
	}
}

func TestBadResponseAfterReadingBody(t *testing.T) {
	defer afterTest(t)
	cst := newClientServerTest(t, HandlerFunc(func(w ResponseWriter, r *Request) {
		_, err := io.Copy(ioutil.Discard, r.Body)
		if err != nil {
			t.Fatal(err)
		}
		c, _, err := w.(Hijacker).Hijack()
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		fmt.Fprintln(c, "some bogus crap")
	}))
	defer cst.close()

	closes := 0
	res, err := cst.c.Post(cst.ts.URL, "text/plain", countCloseReader{&closes, strings.NewReader("hello")})
	if err == nil {
		res.CloseBody()
		t.Fatal("expected an error to be returned from Post")
	}
	if closes != 1 {
		t.Errorf("closes = %d; want 1", closes)
	}
}
