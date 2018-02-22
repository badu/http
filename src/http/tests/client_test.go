/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package tests

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	. "http"
	"http/cli"
	"http/th"
)

func TestClient(t *testing.T) {
	setParallel(t)
	defer afterTest(t)
	ts := th.NewServer(robotsTxtHandler)
	defer ts.Close()

	c := ts.Client()
	r, err := c.Get(ts.URL)
	var b []byte
	if err == nil {
		b, err = pedanticReadAll(r.Body)
		r.Body.Close()
	}
	if err != nil {
		t.Error(err)
	} else if s := string(b); !strings.HasPrefix(s, "User-agent:") {
		t.Errorf("Incorrect page body (did not begin with User-agent): %q", s)
	}
}

func TestClientHead(t *testing.T) {
	defer afterTest(t)
	cst := newClientServerTest(t, robotsTxtHandler)
	defer cst.close()

	r, err := cst.c.Head(cst.ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := r.Header[LastModified]; !ok {
		t.Error("Last-Modified header not found.")
	}
}

func TestGetRequestFormat(t *testing.T) {
	setParallel(t)
	defer afterTest(t)
	tr := &recordingTransport{}
	client := &cli.Client{Transport: tr}
	testUrl := "http://dummy.faketld/"
	client.Get(testUrl) // Note: doesn't hit network
	if tr.req.Method != GET {
		t.Errorf("expected method %q; got %q", GET, tr.req.Method)
	}
	if tr.req.URL.String() != testUrl {
		t.Errorf("expected URL %q; got %q", testUrl, tr.req.URL.String())
	}
	if tr.req.Header == nil {
		t.Errorf("expected non-nil request Header")
	}
}

func TestPostRequestFormat(t *testing.T) {
	defer afterTest(t)
	tr := &recordingTransport{}
	client := &cli.Client{Transport: tr}

	testUrl := "http://dummy.faketld/"
	json := `{"key":"value"}`
	b := strings.NewReader(json)
	client.Post(testUrl, "application/json", b) // Note: doesn't hit network

	if tr.req.Method != POST {
		t.Errorf("got method %q, want %q", tr.req.Method, POST)
	}
	if tr.req.URL.String() != testUrl {
		t.Errorf("got URL %q, want %q", tr.req.URL.String(), testUrl)
	}
	if tr.req.Header == nil {
		t.Fatalf("expected non-nil request Header")
	}
	if tr.req.Close {
		t.Error("got Close true, want false")
	}
	if g, e := tr.req.ContentLength, int64(len(json)); g != e {
		t.Errorf("got ContentLength %d, want %d", g, e)
	}
}

func TestPostFormRequestFormat(t *testing.T) {
	defer afterTest(t)
	tr := &recordingTransport{}
	client := &cli.Client{Transport: tr}

	urlStr := "http://dummy.faketld/"
	form := make(url.Values)
	form.Set("foo", "bar")
	form.Add("foo", "bar2")
	form.Set("bar", "baz")
	client.PostForm(urlStr, form) // Note: doesn't hit network

	if tr.req.Method != POST {
		t.Errorf("got method %q, want %q", tr.req.Method, POST)
	}
	if tr.req.URL.String() != urlStr {
		t.Errorf("got URL %q, want %q", tr.req.URL.String(), urlStr)
	}
	if tr.req.Header == nil {
		t.Fatalf("expected non-nil request Header")
	}
	if g, e := tr.req.Header.Get(ContentType), "application/x-www-form-urlencoded"; g != e {
		t.Errorf("got Content-Type %q, want %q", g, e)
	}
	if tr.req.Close {
		t.Error("got Close true, want false")
	}
	// Depending on map iteration, body can be either of these.
	expectedBody := "foo=bar&foo=bar2&bar=baz"
	expectedBody1 := "bar=baz&foo=bar&foo=bar2"
	if g, e := tr.req.ContentLength, int64(len(expectedBody)); g != e {
		t.Errorf("got ContentLength %d, want %d", g, e)
	}
	bodyb, err := ioutil.ReadAll(tr.req.Body)
	if err != nil {
		t.Fatalf("ReadAll on req.Body: %v", err)
	}
	if g := string(bodyb); g != expectedBody && g != expectedBody1 {
		t.Errorf("got body %q, want %q or %q", g, expectedBody, expectedBody1)
	}
}

func TestClientRedirects(t *testing.T) {
	setParallel(t)
	defer afterTest(t)
	var ts *th.TServer
	ts = th.NewServer(HandlerFunc(func(w ResponseWriter, r *Request) {
		n, _ := strconv.Atoi(r.FormValue("n"))
		// Test Referer header. (7 is arbitrary position to test at)
		if n == 7 {
			if g, e := r.Referer(), ts.URL+"/?n=6"; e != g {
				t.Errorf("on request ?n=7, expected referer of %q; got %q", e, g)
			}
		}
		if n < 15 {
			Redirect(w, r, fmt.Sprintf("/?n=%d", n+1), StatusTemporaryRedirect)
			return
		}
		fmt.Fprintf(w, "n=%d", n)
	}))
	defer ts.Close()

	c := ts.Client()
	_, err := c.Get(ts.URL)
	if e, g := "Get /?n=10: stopped after 10 redirects", fmt.Sprintf("%v", err); e != g {
		t.Errorf("with default client Get, expected error %q, got %q", e, g)
	}

	// HEAD request should also have the ability to follow redirects.
	_, err = c.Head(ts.URL)
	if e, g := "Head /?n=10: stopped after 10 redirects", fmt.Sprintf("%v", err); e != g {
		t.Errorf("with default client Head, expected error %q, got %q", e, g)
	}

	// Do should also follow redirects.
	greq, _ := NewRequest(GET, ts.URL, nil)
	_, err = c.Do(greq)
	if e, g := "Get /?n=10: stopped after 10 redirects", fmt.Sprintf("%v", err); e != g {
		t.Errorf("with default client Do, expected error %q, got %q", e, g)
	}

	// Requests with an empty Method should also redirect (Issue 12705)
	greq.Method = ""
	_, err = c.Do(greq)
	if e, g := "Get /?n=10: stopped after 10 redirects", fmt.Sprintf("%v", err); e != g {
		t.Errorf("with default client Do and empty Method, expected error %q, got %q", e, g)
	}

	var checkErr error
	var lastVia []*Request
	var lastReq *Request
	c.CheckRedirect = func(req *Request, via []*Request) error {
		lastReq = req
		lastVia = via
		return checkErr
	}
	res, err := c.Get(ts.URL)
	if err != nil {
		t.Fatalf("Get error: %v", err)
	}
	res.Body.Close()
	finalUrl := res.Request.URL.String()
	if e, g := "<nil>", fmt.Sprintf("%v", err); e != g {
		t.Errorf("with custom client, expected error %q, got %q", e, g)
	}
	if !strings.HasSuffix(finalUrl, "/?n=15") {
		t.Errorf("expected final url to end in /?n=15; got url %q", finalUrl)
	}
	if e, g := 15, len(lastVia); e != g {
		t.Errorf("expected lastVia to have contained %d elements; got %d", e, g)
	}

	// Test that Request.Cancel is propagated between requests (Issue 14053)
	creq, _ := NewRequest(HEAD, ts.URL, nil)
	//cancel := make(chan struct{})
	//creq.Cancel = cancel
	if _, err := c.Do(creq); err != nil {
		t.Fatal(err)
	}
	if lastReq == nil {
		t.Fatal("didn't see redirect")
	}
	//if lastReq.Cancel != cancel {
	//	t.Errorf("expected lastReq to have the cancel channel set on the initial req")
	//}

	checkErr = errors.New("no redirects allowed")
	res, err = c.Get(ts.URL)
	if urlError, ok := err.(*url.Error); !ok || urlError.Err != checkErr {
		t.Errorf("with redirects forbidden, expected a *url.Error with our 'no redirects allowed' error inside; got %#v (%q)", err, err)
	}
	if res == nil {
		t.Fatalf("Expected a non-nil Response on CheckRedirect failure (https://golang.org/issue/3795)")
	}
	res.Body.Close()
	if res.Header.Get(Location) == "" {
		t.Errorf("no Location header in Response")
	}
}

// Tests that Client redirects' contexts are derived from the original request's context.
func TestClientRedirectContext(t *testing.T) {
	setParallel(t)
	defer afterTest(t)
	ts := th.NewServer(HandlerFunc(func(w ResponseWriter, r *Request) {
		Redirect(w, r, "/", StatusTemporaryRedirect)
	}))
	defer ts.Close()

	ctx, cancel := context.WithCancel(context.Background())
	c := ts.Client()
	c.CheckRedirect = func(req *Request, via []*Request) error {
		cancel()
		select {
		case <-req.Context().Done():
			return nil
		case <-time.After(5 * time.Second):
			return errors.New("redirected request's context never expired after root request canceled")
		}
	}
	req, _ := NewRequest(GET, ts.URL, nil)
	req = req.WithContext(ctx)
	_, err := c.Do(req)
	ue, ok := err.(*url.Error)
	if !ok {
		t.Fatalf("got error %T; want *url.Error", err)
	}
	if ue.Err != context.Canceled {
		t.Errorf("url.Error.Err = %v; want %v", ue.Err, context.Canceled)
	}
}

func TestPostRedirects(t *testing.T) {
	postRedirectTests := []redirectTest{
		{"/", 200, "first"},
		{"/?code=301&next=302", 200, "c301"},
		{"/?code=302&next=302", 200, "c302"},
		{"/?code=303&next=301", 200, "c303wc301"}, // Issue 9348
		{"/?code=304", 304, "c304"},
		{"/?code=305", 305, "c305"},
		{"/?code=307&next=303,308,302", 200, "c307"},
		{"/?code=308&next=302,301", 200, "c308"},
		{"/?code=404", 404, "c404"},
	}

	wantSegments := []string{
		`POST / "first"`,
		`POST /?code=301&next=302 "c301"`,
		`GET /?code=302 ""`,
		`GET / ""`,
		`POST /?code=302&next=302 "c302"`,
		`GET /?code=302 ""`,
		`GET / ""`,
		`POST /?code=303&next=301 "c303wc301"`,
		`GET /?code=301 ""`,
		`GET / ""`,
		`POST /?code=304 "c304"`,
		`POST /?code=305 "c305"`,
		`POST /?code=307&next=303,308,302 "c307"`,
		`POST /?code=303&next=308,302 "c307"`,
		`GET /?code=308&next=302 ""`,
		`GET /?code=302 "c307"`,
		`GET / ""`,
		`POST /?code=308&next=302,301 "c308"`,
		`POST /?code=302&next=301 "c308"`,
		`GET /?code=301 ""`,
		`GET / ""`,
		`POST /?code=404 "c404"`,
	}
	want := strings.Join(wantSegments, "\n")
	testRedirectsByMethod(t, POST, postRedirectTests, want)
}

func TestDeleteRedirects(t *testing.T) {
	deleteRedirectTests := []redirectTest{
		{"/", 200, "first"},
		{"/?code=301&next=302,308", 200, "c301"},
		{"/?code=302&next=302", 200, "c302"},
		{"/?code=303", 200, "c303"},
		{"/?code=307&next=301,308,303,302,304", 304, "c307"},
		{"/?code=308&next=307", 200, "c308"},
		{"/?code=404", 404, "c404"},
	}

	wantSegments := []string{
		`DELETE / "first"`,
		`DELETE /?code=301&next=302,308 "c301"`,
		`GET /?code=302&next=308 ""`,
		`GET /?code=308 ""`,
		`GET / "c301"`,
		`DELETE /?code=302&next=302 "c302"`,
		`GET /?code=302 ""`,
		`GET / ""`,
		`DELETE /?code=303 "c303"`,
		`GET / ""`,
		`DELETE /?code=307&next=301,308,303,302,304 "c307"`,
		`DELETE /?code=301&next=308,303,302,304 "c307"`,
		`GET /?code=308&next=303,302,304 ""`,
		`GET /?code=303&next=302,304 "c307"`,
		`GET /?code=302&next=304 ""`,
		`GET /?code=304 ""`,
		`DELETE /?code=308&next=307 "c308"`,
		`DELETE /?code=307 "c308"`,
		`DELETE / "c308"`,
		`DELETE /?code=404 "c404"`,
	}
	want := strings.Join(wantSegments, "\n")
	testRedirectsByMethod(t, DELETE, deleteRedirectTests, want)
}

func testRedirectsByMethod(t *testing.T, method string, table []redirectTest, want string) {
	defer afterTest(t)
	var sLog struct {
		sync.Mutex
		bytes.Buffer
	}
	var ts *th.TServer
	ts = th.NewServer(HandlerFunc(func(w ResponseWriter, r *Request) {
		sLog.Lock()
		slurp, _ := ioutil.ReadAll(r.Body)
		fmt.Fprintf(&sLog.Buffer, "%s %s %q", r.Method, r.RequestURI, slurp)
		if cl := r.Header.Get(ContentLength); r.Method == GET && len(slurp) == 0 && (r.ContentLength != 0 || cl != "") {
			fmt.Fprintf(&sLog.Buffer, " (but with body=%T, content-length = %v, %q)", r.Body, r.ContentLength, cl)
		}
		sLog.WriteByte('\n')
		sLog.Unlock()
		urlQuery := r.URL.Query()
		if v := urlQuery.Get("code"); v != "" {
			location := ts.URL
			if final := urlQuery.Get("next"); final != "" {
				splits := strings.Split(final, ",")
				first, rest := splits[0], splits[1:]
				location = fmt.Sprintf("%s?code=%s", location, first)
				if len(rest) > 0 {
					location = fmt.Sprintf("%s&next=%s", location, strings.Join(rest, ","))
				}
			}
			code, _ := strconv.Atoi(v)
			if code/100 == 3 {
				w.Header().Set(Location, location)
			}
			w.WriteHeader(code)
		}
	}))
	defer ts.Close()

	c := ts.Client()
	for _, tt := range table {
		content := tt.redirectBody
		req, _ := NewRequest(method, ts.URL+tt.suffix, strings.NewReader(content))
		req.GetBody = func() (io.ReadCloser, error) { return ioutil.NopCloser(strings.NewReader(content)), nil }
		res, err := c.Do(req)

		if err != nil {
			t.Fatal(err)
		}
		if res.StatusCode != tt.want {
			t.Errorf("POST %s: status code = %d; want %d", tt.suffix, res.StatusCode, tt.want)
		}
	}
	sLog.Lock()
	got := sLog.String()
	sLog.Unlock()

	got = strings.TrimSpace(got)
	want = strings.TrimSpace(want)

	if got != want {
		got, want, lines := removeCommonLines(got, want)
		t.Errorf("Log differs after %d common lines.\n\nGot:\n%s\n\nWant:\n%s\n", lines, got, want)
	}
}

func TestClientRedirectUseResponse(t *testing.T) {
	setParallel(t)
	defer afterTest(t)
	const body = "Hello, world."
	var ts *th.TServer
	ts = th.NewServer(HandlerFunc(func(w ResponseWriter, r *Request) {
		if strings.Contains(r.URL.Path, "/other") {
			io.WriteString(w, "wrong body")
		} else {
			w.Header().Set(Location, ts.URL+"/other")
			w.WriteHeader(StatusFound)
			io.WriteString(w, body)
		}
	}))
	defer ts.Close()

	c := ts.Client()
	c.CheckRedirect = func(req *Request, via []*Request) error {
		if req.Response == nil {
			t.Error("expected non-nil Request.Response")
		}
		return cli.ErrUseLastResponse
	}
	res, err := c.Get(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != StatusFound {
		t.Errorf("status = %d; want %d", res.StatusCode, StatusFound)
	}
	defer res.Body.Close()
	slurp, err := ioutil.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(slurp) != body {
		t.Errorf("body = %q; want %q", slurp, body)
	}
}

// Issue 17773: don't follow a 308 (or 307) if the response doesn't
// have a Location header.
func TestClientRedirect308NoLocation(t *testing.T) {
	setParallel(t)
	defer afterTest(t)
	ts := th.NewServer(HandlerFunc(func(w ResponseWriter, r *Request) {
		w.Header().Set("Foo", "Bar")
		w.WriteHeader(308)
	}))
	defer ts.Close()
	c := ts.Client()
	res, err := c.Get(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 308 {
		t.Errorf("status = %d; want %d", res.StatusCode, 308)
	}
	if got := res.Header.Get("Foo"); got != "Bar" {
		t.Errorf("Foo header = %q; want Bar", got)
	}
}

// Don't follow a 307/308 if we can't resent the request body.
func TestClientRedirect308NoGetBody(t *testing.T) {
	setParallel(t)
	defer afterTest(t)
	const fakeURL = "https://localhost:1234/" // won't be hit
	ts := th.NewServer(HandlerFunc(func(w ResponseWriter, r *Request) {
		w.Header().Set(Location, fakeURL)
		w.WriteHeader(308)
	}))
	defer ts.Close()
	req, err := NewRequest(POST, ts.URL, strings.NewReader("some body"))
	if err != nil {
		t.Fatal(err)
	}
	c := ts.Client()
	req.GetBody = nil // so it can't rewind.
	res, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 308 {
		t.Errorf("status = %d; want %d", res.StatusCode, 308)
	}
	if got := res.Header.Get(Location); got != fakeURL {
		t.Errorf("Location header = %q; want %q", got, fakeURL)
	}
}

func TestClientSendsCookieFromJar(t *testing.T) {
	defer afterTest(t)
	tr := &recordingTransport{}
	c := &cli.Client{Transport: tr}
	c.Jar = &TestJar{perURL: make(map[string][]*cli.Cookie)}
	us := "http://dummy.faketld/"
	u, _ := url.Parse(us)
	c.Jar.SetCookies(u, expectedCookies)

	c.Get(us) // Note: doesn't hit network
	matchReturnedCookies(t, expectedCookies, cli.ReqCookies(tr.req))

	c.Head(us) // Note: doesn't hit network
	matchReturnedCookies(t, expectedCookies, cli.ReqCookies(tr.req))

	c.Post(us, "text/plain", strings.NewReader("body")) // Note: doesn't hit network
	matchReturnedCookies(t, expectedCookies, cli.ReqCookies(tr.req))

	c.PostForm(us, url.Values{}) // Note: doesn't hit network
	matchReturnedCookies(t, expectedCookies, cli.ReqCookies(tr.req))

	req, _ := NewRequest(GET, us, nil)
	c.Do(req) // Note: doesn't hit network
	matchReturnedCookies(t, expectedCookies, cli.ReqCookies(tr.req))

	req, _ = NewRequest(POST, us, nil)
	c.Do(req) // Note: doesn't hit network
	matchReturnedCookies(t, expectedCookies, cli.ReqCookies(tr.req))
}

func TestRedirectCookiesJar(t *testing.T) {

	var echoCookiesRedirectHandler = HandlerFunc(func(w ResponseWriter, r *Request) {
		for _, cookie := range cli.ReqCookies(r) {
			cli.SetCookie(w, cookie)
		}
		if r.URL.Path == "/" {
			cli.SetCookie(w, expectedCookies[1])
			Redirect(w, r, "/second", StatusMovedPermanently)
		} else {
			cli.SetCookie(w, expectedCookies[2])
			w.Write([]byte("hello"))
		}
	})

	setParallel(t)
	defer afterTest(t)
	var ts *th.TServer
	ts = th.NewServer(echoCookiesRedirectHandler)
	defer ts.Close()
	c := ts.Client()
	c.Jar = new(TestJar)
	u, _ := url.Parse(ts.URL)
	c.Jar.SetCookies(u, []*cli.Cookie{expectedCookies[0]})
	resp, err := c.Get(ts.URL)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	resp.Body.Close()
	matchReturnedCookies(t, expectedCookies, cli.RespCookies(resp))
}

func TestJarCalls(t *testing.T) {
	defer afterTest(t)
	ts := th.NewServer(HandlerFunc(func(w ResponseWriter, r *Request) {
		pathSuffix := r.RequestURI[1:]
		if r.RequestURI == "/nosetcookie" {
			return // don't set cookies for this path
		}
		cli.SetCookie(w, &cli.Cookie{Name: "name" + pathSuffix, Value: "val" + pathSuffix})
		if r.RequestURI == "/" {
			Redirect(w, r, "http://secondhost.fake/secondpath", 302)
		}
	}))
	defer ts.Close()
	jar := new(RecordingJar)
	c := ts.Client()
	c.Jar = jar
	c.Transport.(*Transport).DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		return net.Dial("tcp", ts.Listener.Addr().String())
	}
	_, err := c.Get("http://firsthost.fake/")
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Get("http://firsthost.fake/nosetcookie")
	if err != nil {
		t.Fatal(err)
	}
	got := jar.log.String()
	want := `Cookies("http://firsthost.fake/")
SetCookie("http://firsthost.fake/", [name=val])
Cookies("http://secondhost.fake/secondpath")
SetCookie("http://secondhost.fake/secondpath", [namesecondpath=valsecondpath])
Cookies("http://firsthost.fake/nosetcookie")
`
	if got != want {
		t.Errorf("Got Jar calls:\n%s\nWant:\n%s", got, want)
	}
}

func TestStreamingGet(t *testing.T) {
	defer afterTest(t)
	say := make(chan string)
	cst := newClientServerTest(t, HandlerFunc(func(w ResponseWriter, r *Request) {
		w.(Flusher).Flush()
		for str := range say {
			w.Write([]byte(str))
			w.(Flusher).Flush()
		}
	}))
	defer cst.close()

	c := cst.c
	res, err := c.Get(cst.ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	var buf [10]byte
	for _, str := range []string{"i", "am", "also", "known", "as", "comet"} {
		say <- str
		n, err := io.ReadFull(res.Body, buf[0:len(str)])
		if err != nil {
			t.Fatalf("ReadFull on %q: %v", str, err)
		}
		if n != len(str) {
			t.Fatalf("Receiving %q, only read %d bytes", str, n)
		}
		got := string(buf[0:n])
		if got != str {
			t.Fatalf("Expected %q, got %q", str, got)
		}
	}
	close(say)
	_, err = io.ReadFull(res.Body, buf[0:1])
	if err != io.EOF {
		t.Fatalf("at end expected EOF, got %v", err)
	}
}

// TestClientWrites verifies that client requests are buffered and we
// don't send a TCP packet per line of the http request + body.
func TestClientWrites(t *testing.T) {
	defer afterTest(t)
	ts := th.NewServer(HandlerFunc(func(w ResponseWriter, r *Request) {
	}))
	defer ts.Close()

	writes := 0
	dialer := func(ctx context.Context, network, addr string) (net.Conn, error) {
		c, err := net.Dial(network, addr)
		if err == nil {
			c = &writeCountingConn{c, &writes}
		}
		return c, err
	}
	c := ts.Client()
	c.Transport.(*Transport).DialContext = dialer

	_, err := c.Get(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	if writes != 1 {
		t.Errorf("Get request did %d Write calls, want 1", writes)
	}

	writes = 0
	_, err = c.PostForm(ts.URL, url.Values{"foo": {"bar"}})
	if err != nil {
		t.Fatal(err)
	}
	if writes != 1 {
		t.Errorf("Post request did %d Write calls, want 1", writes)
	}
}

func TestClientInsecureTransport(t *testing.T) {
	setParallel(t)
	defer afterTest(t)
	ts := th.NewTLSServer(HandlerFunc(func(w ResponseWriter, r *Request) {
		w.Write([]byte("Hello"))
	}))
	errc := make(chanWriter, 10) // but only expecting 1
	ts.Config.ErrorLog = log.New(errc, "", 0)
	defer ts.Close()

	// TODO(bradfitz): add tests for skipping hostname checks too?
	// would require a new cert for testing, and probably
	// redundant with these tests.
	c := ts.Client()
	for _, insecure := range []bool{true, false} {
		c.Transport.(*Transport).TLSClientConfig = &tls.Config{
			InsecureSkipVerify: insecure,
		}
		res, err := c.Get(ts.URL)
		if (err == nil) != insecure {
			t.Errorf("insecure=%v: got unexpected err=%v", insecure, err)
		}
		if res != nil {
			res.Body.Close()
		}
	}

	select {
	case v := <-errc:
		if !strings.Contains(v, "TLS handshake error") {
			t.Errorf("expected an error log message containing 'TLS handshake error'; got %q", v)
		}
	case <-time.After(5 * time.Second):
		t.Errorf("timeout waiting for logged error")
	}
}

func TestClientErrorWithRequestURI(t *testing.T) {
	defer afterTest(t)
	req, _ := NewRequest(GET, "http://localhost:1234/", nil)
	req.RequestURI = "/this/field/is/illegal/and/should/error/"
	_, err := cli.DefaultClient.Do(req)
	if err == nil {
		t.Fatalf("expected an error")
	}
	if !strings.Contains(err.Error(), "RequestURI") {
		t.Errorf("wanted error mentioning RequestURI; got error: %v", err)
	}
}

func TestClientWithCorrectTLSServerName(t *testing.T) {
	defer afterTest(t)

	const serverName = "example.com"
	ts := th.NewTLSServer(HandlerFunc(func(w ResponseWriter, r *Request) {
		if r.TLS.ServerName != serverName {
			t.Errorf("expected client to set ServerName %q, got: %q", serverName, r.TLS.ServerName)
		}
	}))
	defer ts.Close()

	c := ts.Client()
	c.Transport.(*Transport).TLSClientConfig.ServerName = serverName
	if _, err := c.Get(ts.URL); err != nil {
		t.Fatalf("expected successful TLS connection, got error: %v", err)
	}
}

func TestClientWithIncorrectTLSServerName(t *testing.T) {
	defer afterTest(t)
	ts := th.NewTLSServer(HandlerFunc(func(w ResponseWriter, r *Request) {}))
	defer ts.Close()
	errc := make(chanWriter, 10) // but only expecting 1
	ts.Config.ErrorLog = log.New(errc, "", 0)

	c := ts.Client()
	c.Transport.(*Transport).TLSClientConfig.ServerName = "badserver"
	_, err := c.Get(ts.URL)
	if err == nil {
		t.Fatalf("expected an error")
	}
	if !strings.Contains(err.Error(), "127.0.0.1") || !strings.Contains(err.Error(), "badserver") {
		t.Errorf("wanted error mentioning 127.0.0.1 and badserver; got error: %v", err)
	}
	select {
	case v := <-errc:
		if !strings.Contains(v, "TLS handshake error") {
			t.Errorf("expected an error log message containing 'TLS handshake error'; got %q", v)
		}
	case <-time.After(5 * time.Second):
		t.Errorf("timeout waiting for logged error")
	}
}

// Test for golang.org/issue/5829; the Transport should respect TLSClientConfig.ServerName
// when not empty.
//
// tls.Config.ServerName (non-empty, set to "example.com") takes
// precedence over "some-other-host.tld" which previously incorrectly
// took precedence. We don't actually connect to (or even resolve)
// "some-other-host.tld", though, because of the Transport.Dial hook.
//
// The testhelper.Server has a cert with "example.com" as its name.
func TestTransportUsesTLSConfigServerName(t *testing.T) {
	defer afterTest(t)
	ts := th.NewTLSServer(HandlerFunc(func(w ResponseWriter, r *Request) {
		w.Write([]byte("Hello"))
	}))
	defer ts.Close()

	c := ts.Client()
	tr := c.Transport.(*Transport)
	tr.TLSClientConfig.ServerName = "example.com" // one of testhelper's Server cert names
	tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		return net.Dial(network, ts.Listener.Addr().String())
	}
	res, err := c.Get("https://some-other-host.tld/")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
}

func TestResponseSetsTLSConnectionState(t *testing.T) {
	defer afterTest(t)
	ts := th.NewTLSServer(HandlerFunc(func(w ResponseWriter, r *Request) {
		w.Write([]byte("Hello"))
	}))
	defer ts.Close()

	c := ts.Client()
	tr := c.Transport.(*Transport)
	tr.TLSClientConfig.CipherSuites = []uint16{tls.TLS_RSA_WITH_3DES_EDE_CBC_SHA}
	tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		return net.Dial(network, ts.Listener.Addr().String())
	}
	res, err := c.Get("https://example.com/")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.TLS == nil {
		t.Fatal("Response didn't set TLS Connection State.")
	}
	if got, want := res.TLS.CipherSuite, tls.TLS_RSA_WITH_3DES_EDE_CBC_SHA; got != want {
		t.Errorf("TLS Cipher Suite = %d; want %d", got, want)
	}
}

// Check that an HTTPS client can interpret a particular TLS error
// to determine that the server is speaking HTTP.
// See golang.org/issue/11111.
func TestHTTPSClientDetectsHTTPServer(t *testing.T) {
	defer afterTest(t)
	ts := th.NewServer(HandlerFunc(func(w ResponseWriter, r *Request) {}))
	ts.Config.ErrorLog = log.New(ioutil.Discard, "", 0)
	defer ts.Close()

	_, err := cli.Get(strings.Replace(ts.URL, HTTP, HTTPS, 1))
	if got := err.Error(); !strings.Contains(got, "HTTP response to HTTPS client") {
		t.Fatalf("error = %q; want error indicating HTTP response to HTTPS request", got)
	}
}

// Verify Response.ContentLength is populated. https://golang.org/issue/4126
func TestClientHeadContentLength(t *testing.T) {
	defer afterTest(t)
	cst := newClientServerTest(t, HandlerFunc(func(w ResponseWriter, r *Request) {
		if v := r.FormValue("cl"); v != "" {
			w.Header().Set(ContentLength, v)
		}
	}))
	defer cst.close()
	tests := []struct {
		suffix string
		want   int64
	}{
		{"/?cl=1234", 1234},
		{"/?cl=0", 0},
		{"", -1},
	}
	for _, tt := range tests {
		req, _ := NewRequest(HEAD, cst.ts.URL+tt.suffix, nil)
		res, err := cst.c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		if res.ContentLength != tt.want {
			t.Errorf("Content-Length = %d; want %d", res.ContentLength, tt.want)
		}
		bs, err := ioutil.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		if len(bs) != 0 {
			t.Errorf("Unexpected content: %q", bs)
		}
	}
}

func TestEmptyPasswordAuth(t *testing.T) {
	setParallel(t)
	defer afterTest(t)
	gopher := "gopher"
	ts := th.NewServer(HandlerFunc(func(w ResponseWriter, r *Request) {
		auth := r.Header.Get(Authorization)
		if strings.HasPrefix(auth, "Basic ") {
			encoded := auth[6:]
			decoded, err := base64.StdEncoding.DecodeString(encoded)
			if err != nil {
				t.Fatal(err)
			}
			expected := gopher + ":"
			s := string(decoded)
			if expected != s {
				t.Errorf("Invalid Authorization header. Got %q, wanted %q", s, expected)
			}
		} else {
			t.Errorf("Invalid auth %q", auth)
		}
	}))
	defer ts.Close()
	req, err := NewRequest(GET, ts.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.URL.User = url.User(gopher)
	c := ts.Client()
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
}

func TestBasicAuth(t *testing.T) {
	defer afterTest(t)
	tr := &recordingTransport{}
	client := &cli.Client{Transport: tr}

	testUrl := "http://My%20User:My%20Pass@dummy.faketld/"
	expected := "My User:My Pass"
	client.Get(testUrl)

	if tr.req.Method != GET {
		t.Errorf("got method %q, want %q", tr.req.Method, GET)
	}
	if tr.req.URL.String() != testUrl {
		t.Errorf("got URL %q, want %q", tr.req.URL.String(), testUrl)
	}
	if tr.req.Header == nil {
		t.Fatalf("expected non-nil request Header")
	}
	auth := tr.req.Header.Get(Authorization)
	if strings.HasPrefix(auth, "Basic ") {
		encoded := auth[6:]
		decoded, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			t.Fatal(err)
		}
		s := string(decoded)
		if expected != s {
			t.Errorf("Invalid Authorization header. Got %q, wanted %q", s, expected)
		}
	} else {
		t.Errorf("Invalid auth %q", auth)
	}
}

func TestBasicAuthHeadersPreserved(t *testing.T) {
	defer afterTest(t)
	tr := &recordingTransport{}
	client := &cli.Client{Transport: tr}

	// If Authorization header is provided, username in URL should not override it
	testUrl := "http://My%20User@dummy.faketld/"
	req, err := NewRequest(GET, testUrl, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.SetBasicAuth("My User", "My Pass")
	expected := "My User:My Pass"
	client.Do(req)

	if tr.req.Method != GET {
		t.Errorf("got method %q, want %q", tr.req.Method, GET)
	}
	if tr.req.URL.String() != testUrl {
		t.Errorf("got URL %q, want %q", tr.req.URL.String(), testUrl)
	}
	if tr.req.Header == nil {
		t.Fatalf("expected non-nil request Header")
	}
	auth := tr.req.Header.Get(Authorization)
	if strings.HasPrefix(auth, "Basic ") {
		encoded := auth[6:]
		decoded, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			t.Fatal(err)
		}
		s := string(decoded)
		if expected != s {
			t.Errorf("Invalid Authorization header. Got %q, wanted %q", s, expected)
		}
	} else {
		t.Errorf("Invalid auth %q", auth)
	}

}

func TestClientRedirectEatsBody(t *testing.T) {
	setParallel(t)
	defer afterTest(t)
	saw := make(chan string, 2)
	cst := newClientServerTest(t, HandlerFunc(func(w ResponseWriter, r *Request) {
		saw <- r.RemoteAddr
		if r.URL.Path == "/" {
			Redirect(w, r, "/foo", StatusFound) // which includes a body
		}
	}))
	defer cst.close()

	res, err := cst.c.Get(cst.ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	_, err = ioutil.ReadAll(res.Body)
	res.Body.Close()
	if err != nil {
		t.Fatal(err)
	}

	var first string
	select {
	case first = <-saw:
	default:
		t.Fatal("server didn't see a request")
	}

	var second string
	select {
	case second = <-saw:
	default:
		t.Fatal("server didn't see a second request")
	}

	if first != second {
		t.Fatal("server saw different client ports before & after the redirect")
	}
}

func TestReferer(t *testing.T) {
	tests := []struct {
		lastReq, newReq string // from -> to URLs
		want            string
	}{
		// don't send user:
		{"http://gopher@test.com", "http://link.com", "http://test.com"},
		{"https://gopher@test.com", "https://link.com", "https://test.com"},

		// don't send a user and password:
		{"http://gopher:go@test.com", "http://link.com", "http://test.com"},
		{"https://gopher:go@test.com", "https://link.com", "https://test.com"},

		// nothing to do:
		{"http://test.com", "http://link.com", "http://test.com"},
		{"https://test.com", "https://link.com", "https://test.com"},

		// https to http doesn't send a referer:
		{"https://test.com", "http://link.com", ""},
		{"https://gopher:go@test.com", "http://link.com", ""},
	}
	for _, tt := range tests {
		l, err := url.Parse(tt.lastReq)
		if err != nil {
			t.Fatal(err)
		}
		n, err := url.Parse(tt.newReq)
		if err != nil {
			t.Fatal(err)
		}
		r := cli.RefererForURL(l, n)
		if r != tt.want {
			t.Errorf("refererForURL(%q, %q) = %q; want %q", tt.lastReq, tt.newReq, r, tt.want)
		}
	}
}

// Issue 15577: don't assume the roundtripper's response populates its Request field.
func TestClientRedirectResponseWithoutRequest(t *testing.T) {
	c := &cli.Client{
		CheckRedirect: func(*Request, []*Request) error { return fmt.Errorf("no redirects!") },
		Transport:     issue15577Tripper{},
	}
	// Check that this doesn't crash:
	c.Get("http://dummy.tld")
}

// Issue 4800: copy (some) headers when Client follows a redirect
func TestClientCopyHeadersOnRedirect(t *testing.T) {
	const (
		ua   = "some-agent/1.2"
		xfoo = "foo-val"
	)
	var ts2URL string
	ts1 := th.NewServer(HandlerFunc(func(w ResponseWriter, r *Request) {
		want := Header{
			UserAgent:      []string{ua},
			"X-Foo":        []string{xfoo},
			Referer:        []string{ts2URL},
			AcceptEncoding: []string{"gzip"},
		}
		if !reflect.DeepEqual(r.Header, want) {
			t.Errorf("Request.Header = %#v; want %#v", r.Header, want)
		}
		if t.Failed() {
			w.Header().Set("Result", "got errors")
		} else {
			w.Header().Set("Result", "ok")
		}
	}))
	defer ts1.Close()
	ts2 := th.NewServer(HandlerFunc(func(w ResponseWriter, r *Request) {
		Redirect(w, r, ts1.URL, StatusFound)
	}))
	defer ts2.Close()
	ts2URL = ts2.URL

	c := ts1.Client()
	c.CheckRedirect = func(r *Request, via []*Request) error {
		want := Header{
			UserAgent: []string{ua},
			"X-Foo":   []string{xfoo},
			Referer:   []string{ts2URL},
		}
		if !reflect.DeepEqual(r.Header, want) {
			t.Errorf("CheckRedirect Request.Header = %#v; want %#v", r.Header, want)
		}
		return nil
	}

	req, _ := NewRequest(GET, ts2.URL, nil)
	req.Header.Add(UserAgent, ua)
	req.Header.Add("X-Foo", xfoo)
	req.Header.Add(CookieHeader, "foo=bar")
	req.Header.Add(Authorization, "secretpassword")
	res, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatal(res.Status)
	}
	if got := res.Header.Get("Result"); got != "ok" {
		t.Errorf("result = %q; want ok", got)
	}
}

// Issue 17494: cookies should be altered when Client follows redirects.
func TestClientAltersCookiesOnRedirect(t *testing.T) {
	cookieMap := func(cs []*cli.Cookie) map[string][]string {
		m := make(map[string][]string)
		for _, c := range cs {
			m[c.Name] = append(m[c.Name], c.Value)
		}
		return m
	}

	ts := th.NewServer(HandlerFunc(func(w ResponseWriter, r *Request) {
		var want map[string][]string
		got := cookieMap(cli.ReqCookies(r))

		c, _ := cli.GetCookie("Cycle", r)
		switch c.Value {
		case "0":
			want = map[string][]string{
				"Cookie1": {"OldValue1a", "OldValue1b"},
				"Cookie2": {"OldValue2"},
				"Cookie3": {"OldValue3a", "OldValue3b"},
				"Cookie4": {"OldValue4"},
				"Cycle":   {"0"},
			}
			cli.SetCookie(w, &cli.Cookie{Name: "Cycle", Value: "1", Path: "/"})
			cli.SetCookie(w, &cli.Cookie{Name: "Cookie2", Path: "/", MaxAge: -1}) // Delete cookie from Header
			Redirect(w, r, "/", StatusFound)
		case "1":
			want = map[string][]string{
				"Cookie1": {"OldValue1a", "OldValue1b"},
				"Cookie3": {"OldValue3a", "OldValue3b"},
				"Cookie4": {"OldValue4"},
				"Cycle":   {"1"},
			}
			cli.SetCookie(w, &cli.Cookie{Name: "Cycle", Value: "2", Path: "/"})
			cli.SetCookie(w, &cli.Cookie{Name: "Cookie3", Value: "NewValue3", Path: "/"}) // Modify cookie in Header
			cli.SetCookie(w, &cli.Cookie{Name: "Cookie4", Value: "NewValue4", Path: "/"}) // Modify cookie in Jar
			Redirect(w, r, "/", StatusFound)
		case "2":
			want = map[string][]string{
				"Cookie1": {"OldValue1a", "OldValue1b"},
				"Cookie3": {"NewValue3"},
				"Cookie4": {"NewValue4"},
				"Cycle":   {"2"},
			}
			cli.SetCookie(w, &cli.Cookie{Name: "Cycle", Value: "3", Path: "/"})
			cli.SetCookie(w, &cli.Cookie{Name: "Cookie5", Value: "NewValue5", Path: "/"}) // Insert cookie into Jar
			Redirect(w, r, "/", StatusFound)
		case "3":
			want = map[string][]string{
				"Cookie1": {"OldValue1a", "OldValue1b"},
				"Cookie3": {"NewValue3"},
				"Cookie4": {"NewValue4"},
				"Cookie5": {"NewValue5"},
				"Cycle":   {"3"},
			}
			// Don't redirect to ensure the loop ends.
		default:
			t.Errorf("unexpected redirect cycle")
			return
		}

		if !reflect.DeepEqual(got, want) {
			t.Errorf("redirect %s, Cookie = %v, want %v", c.Value, got, want)
		}
	}))
	defer ts.Close()

	jar, _ := cli.NewCookie(nil)
	c := ts.Client()
	c.Jar = jar

	u, _ := url.Parse(ts.URL)
	req, _ := NewRequest(GET, ts.URL, nil)
	cli.AddCookie(&cli.Cookie{Name: "Cookie1", Value: "OldValue1a"}, req)
	cli.AddCookie(&cli.Cookie{Name: "Cookie1", Value: "OldValue1b"}, req)
	cli.AddCookie(&cli.Cookie{Name: "Cookie2", Value: "OldValue2"}, req)
	cli.AddCookie(&cli.Cookie{Name: "Cookie3", Value: "OldValue3a"}, req)
	cli.AddCookie(&cli.Cookie{Name: "Cookie3", Value: "OldValue3b"}, req)
	jar.SetCookies(u, []*cli.Cookie{{Name: "Cookie4", Value: "OldValue4", Path: "/"}})
	jar.SetCookies(u, []*cli.Cookie{{Name: "Cycle", Value: "0", Path: "/"}})
	res, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatal(res.Status)
	}
}

// Part of Issue 4800
func TestShouldCopyHeaderOnRedirect(t *testing.T) {
	tests := []struct {
		header     string
		initialURL string
		destURL    string
		want       bool
	}{
		{UserAgent, "http://foo.com/", "http://bar.com/", true},
		{"X-Foo", "http://foo.com/", "http://bar.com/", true},

		// Sensitive headers:
		{"cookie", "http://foo.com/", "http://bar.com/", false},
		{"cookie2", "http://foo.com/", "http://bar.com/", false},
		{"authorization", "http://foo.com/", "http://bar.com/", false},
		{"www-authenticate", "http://foo.com/", "http://bar.com/", false},

		// But subdomains should work:
		{"www-authenticate", "http://foo.com/", "http://foo.com/", true},
		{"www-authenticate", "http://foo.com/", "http://sub.foo.com/", true},
		{"www-authenticate", "http://foo.com/", "http://notfoo.com/", false},
		// TODO(bradfitz): make this test work, once issue 16142 is fixed:
		// {"www-authenticate", "http://foo.com:80/", "http://foo.com/", true},
	}
	for i, tt := range tests {
		u0, err := url.Parse(tt.initialURL)
		if err != nil {
			t.Errorf("%d. initial URL %q parse error: %v", i, tt.initialURL, err)
			continue
		}
		u1, err := url.Parse(tt.destURL)
		if err != nil {
			t.Errorf("%d. dest URL %q parse error: %v", i, tt.destURL, err)
			continue
		}
		got := cli.ShouldCopyHeaderOnRedirect(tt.header, u0, u1)
		if got != tt.want {
			t.Errorf("%d. shouldCopyHeaderOnRedirect(%q, %q => %q) = %v; want %v",
				i, tt.header, tt.initialURL, tt.destURL, got, tt.want)
		}
	}
}

func TestClientRedirectTypes(t *testing.T) {
	setParallel(t)
	defer afterTest(t)

	tests := [...]struct {
		method       string
		serverStatus int
		wantMethod   string // desired subsequent client method
	}{
		0: {method: POST, serverStatus: 301, wantMethod: GET},
		1: {method: POST, serverStatus: 302, wantMethod: GET},
		2: {method: POST, serverStatus: 303, wantMethod: GET},
		3: {method: POST, serverStatus: 307, wantMethod: POST},
		4: {method: POST, serverStatus: 308, wantMethod: POST},

		5: {method: HEAD, serverStatus: 301, wantMethod: HEAD},
		6: {method: HEAD, serverStatus: 302, wantMethod: HEAD},
		7: {method: HEAD, serverStatus: 303, wantMethod: HEAD},
		8: {method: HEAD, serverStatus: 307, wantMethod: HEAD},
		9: {method: HEAD, serverStatus: 308, wantMethod: HEAD},

		10: {method: GET, serverStatus: 301, wantMethod: GET},
		11: {method: GET, serverStatus: 302, wantMethod: GET},
		12: {method: GET, serverStatus: 303, wantMethod: GET},
		13: {method: GET, serverStatus: 307, wantMethod: GET},
		14: {method: GET, serverStatus: 308, wantMethod: GET},

		15: {method: DELETE, serverStatus: 301, wantMethod: GET},
		16: {method: DELETE, serverStatus: 302, wantMethod: GET},
		17: {method: DELETE, serverStatus: 303, wantMethod: GET},
		18: {method: DELETE, serverStatus: 307, wantMethod: DELETE},
		19: {method: DELETE, serverStatus: 308, wantMethod: DELETE},

		20: {method: PUT, serverStatus: 301, wantMethod: GET},
		21: {method: PUT, serverStatus: 302, wantMethod: GET},
		22: {method: PUT, serverStatus: 303, wantMethod: GET},
		23: {method: PUT, serverStatus: 307, wantMethod: PUT},
		24: {method: PUT, serverStatus: 308, wantMethod: PUT},

		25: {method: "MADEUPMETHOD", serverStatus: 301, wantMethod: GET},
		26: {method: "MADEUPMETHOD", serverStatus: 302, wantMethod: GET},
		27: {method: "MADEUPMETHOD", serverStatus: 303, wantMethod: GET},
		28: {method: "MADEUPMETHOD", serverStatus: 307, wantMethod: "MADEUPMETHOD"},
		29: {method: "MADEUPMETHOD", serverStatus: 308, wantMethod: "MADEUPMETHOD"},
	}

	handlerc := make(chan HandlerFunc, 1)

	ts := th.NewServer(HandlerFunc(func(rw ResponseWriter, req *Request) {
		h := <-handlerc
		h(rw, req)
	}))
	defer ts.Close()

	c := ts.Client()
	for i, tt := range tests {
		handlerc <- func(w ResponseWriter, r *Request) {
			w.Header().Set(Location, ts.URL)
			w.WriteHeader(tt.serverStatus)
		}

		req, err := NewRequest(tt.method, ts.URL, nil)
		if err != nil {
			t.Errorf("#%d: NewRequest: %v", i, err)
			continue
		}

		c.CheckRedirect = func(req *Request, via []*Request) error {
			if got, want := req.Method, tt.wantMethod; got != want {
				return fmt.Errorf("#%d: got next method %q; want %q", i, got, want)
			}
			handlerc <- func(rw ResponseWriter, req *Request) {
				// TODO: Check that the body is valid when we do 307 and 308 support
			}
			return nil
		}

		res, err := c.Do(req)
		if err != nil {
			t.Errorf("#%d: Response: %v", i, err)
			continue
		}

		res.Body.Close()
	}
}

// Issue 18239: make sure the Transport doesn't retry requests with bodies
// if Request.GetBody is not defined.
func TestTransportBodyReadError(t *testing.T) {
	setParallel(t)
	defer afterTest(t)
	ts := th.NewServer(HandlerFunc(func(w ResponseWriter, r *Request) {
		if r.URL.Path == "/ping" {
			return
		}
		buf := make([]byte, 1)
		n, err := r.Body.Read(buf)
		w.Header().Set("X-Body-Read", fmt.Sprintf("%v, %v", n, err))
	}))
	defer ts.Close()
	c := ts.Client()
	tr := c.Transport.(*Transport)

	// Do one initial successful request to create an idle TCP connection
	// for the subsequent request to reuse. (The Transport only retries
	// requests on reused connections.)
	res, err := c.Get(ts.URL + "/ping")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()

	var readCallsAtomic int32
	var closeCallsAtomic int32 // atomic
	someErr := errors.New("some body read error")
	body := issue18239Body{&readCallsAtomic, &closeCallsAtomic, someErr}

	req, err := NewRequest(POST, ts.URL, body)
	if err != nil {
		t.Fatal(err)
	}
	req = req.WithT(t)
	_, err = tr.RoundTrip(req)
	if err != someErr {
		t.Errorf("Got error: %v; want Request.Body read error: %v", err, someErr)
	}

	// And verify that our Body wasn't used multiple times, which
	// would indicate retries. (as it buggily was during part of
	// Go 1.8's dev cycle)
	readCalls := atomic.LoadInt32(&readCallsAtomic)
	closeCalls := atomic.LoadInt32(&closeCallsAtomic)
	if readCalls != 1 {
		t.Errorf("read calls = %d; want 1", readCalls)
	}
	if closeCalls != 1 {
		t.Errorf("close calls = %d; want 1", closeCalls)
	}
}
