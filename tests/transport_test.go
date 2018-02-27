/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package tests

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net"
	"net/url"
	"os"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	. "http"
	"http/cli"
	"http/mux"
	"http/th"
	. "http/tport"
	"http/trc"
	"http/util"
)

// Two subsequent requests and verify their response is the same.
// The response from the server is our own IP:port
func TestTransportKeepAlives(t *testing.T) {
	defer afterTest(t)
	ts := th.NewServer(hostPortHandler)
	defer ts.Close()

	c := ts.Client()
	for _, disableKeepAlive := range []bool{false, true} {
		c.Transport.(*Transport).DisableKeepAlives = disableKeepAlive
		fetch := func(n int) string {
			res, err := c.Get(ts.URL)
			if err != nil {
				t.Fatalf("error in disableKeepAlive=%v, req #%d, GET: %v", disableKeepAlive, n, err)
			}
			body, err := ioutil.ReadAll(res.Body)
			if err != nil {
				t.Fatalf("error in disableKeepAlive=%v, req #%d, ReadAll: %v", disableKeepAlive, n, err)
			}
			return string(body)
		}

		body1 := fetch(1)
		body2 := fetch(2)

		bodiesDiffer := body1 != body2
		if bodiesDiffer != disableKeepAlive {
			t.Errorf("error in disableKeepAlive=%v. unexpected bodiesDiffer=%v; body1=%q; body2=%q",
				disableKeepAlive, bodiesDiffer, body1, body2)
		}
	}
}

func TestTransportConnectionCloseOnResponse(t *testing.T) {
	defer afterTest(t)
	ts := th.NewServer(hostPortHandler)
	defer ts.Close()

	connSet, testDial := makeTestDial(t)

	c := ts.Client()
	tr := c.Transport.(*Transport)
	tr.DialContext = testDial

	for _, connectionClose := range []bool{false, true} {
		fetch := func(n int) string {
			req := new(Request)
			var err error
			req.URL, err = url.Parse(ts.URL + fmt.Sprintf("/?close=%v", connectionClose))
			if err != nil {
				t.Fatalf("URL parse error: %v", err)
			}
			req.Method = GET
			req.Proto = HTTP1_1
			req.ProtoMajor = 1
			req.ProtoMinor = 1

			res, err := c.Do(req)
			if err != nil {
				t.Fatalf("error in connectionClose=%v, req #%d, Do: %v", connectionClose, n, err)
			}
			defer res.CloseBody()
			body, err := ioutil.ReadAll(res.Body)
			if err != nil {
				t.Fatalf("error in connectionClose=%v, req #%d, ReadAll: %v", connectionClose, n, err)
			}
			return string(body)
		}

		body1 := fetch(1)
		body2 := fetch(2)
		bodiesDiffer := body1 != body2
		if bodiesDiffer != connectionClose {
			t.Errorf("error in connectionClose=%v. unexpected bodiesDiffer=%v; body1=%q; body2=%q",
				connectionClose, bodiesDiffer, body1, body2)
		}

		tr.CloseIdleConnections()
	}

	connSet.check(t)
}

func TestTransportConnectionCloseOnRequest(t *testing.T) {
	defer afterTest(t)
	ts := th.NewServer(hostPortHandler)
	defer ts.Close()

	connSet, testDial := makeTestDial(t)

	c := ts.Client()
	tr := c.Transport.(*Transport)
	tr.DialContext = testDial
	for _, connectionClose := range []bool{false, true} {
		fetch := func(n int) string {
			req := new(Request)
			var err error
			req.URL, err = url.Parse(ts.URL)
			if err != nil {
				t.Fatalf("URL parse error: %v", err)
			}
			req.Method = GET
			req.Proto = HTTP1_1
			req.ProtoMajor = 1
			req.ProtoMinor = 1
			req.Close = connectionClose

			res, err := c.Do(req)
			if err != nil {
				t.Fatalf("error in connectionClose=%v, req #%d, Do: %v", connectionClose, n, err)
			}
			if got, want := res.Header.Get("X-Saw-Close"), fmt.Sprint(connectionClose); got != want {
				t.Errorf("For connectionClose = %v; handler's X-Saw-Close was %v; want %v",
					connectionClose, got, !connectionClose)
			}
			body, err := ioutil.ReadAll(res.Body)
			if err != nil {
				t.Fatalf("error in connectionClose=%v, req #%d, ReadAll: %v", connectionClose, n, err)
			}
			return string(body)
		}

		body1 := fetch(1)
		body2 := fetch(2)
		bodiesDiffer := body1 != body2
		if bodiesDiffer != connectionClose {
			t.Errorf("error in connectionClose=%v. unexpected bodiesDiffer=%v; body1=%q; body2=%q",
				connectionClose, bodiesDiffer, body1, body2)
		}

		tr.CloseIdleConnections()
	}

	connSet.check(t)
}

// if the Transport's DisableKeepAlives is set, all requests should
// send Connection: close.
// HTTP/1-only (Connection: close doesn't exist in h2)
func TestTransportConnectionCloseOnRequestDisableKeepAlive(t *testing.T) {
	defer afterTest(t)
	ts := th.NewServer(hostPortHandler)
	defer ts.Close()

	c := ts.Client()
	c.Transport.(*Transport).DisableKeepAlives = true

	res, err := c.Get(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	res.CloseBody()
	if res.Header.Get("X-Saw-Close") != "true" {
		t.Errorf("handler didn't see Connection: close ")
	}
}

func TestTransportIdleCacheKeys(t *testing.T) {
	defer afterTest(t)
	ts := th.NewServer(hostPortHandler)
	defer ts.Close()
	c := ts.Client()
	tr := c.Transport.(*Transport)

	if e, g := 0, len(tr.IdleConnKeysForTesting()); e != g {
		t.Errorf("After CloseIdleConnections expected %d idle conn cache keys; got %d", e, g)
	}

	resp, err := c.Get(ts.URL)
	if err != nil {
		t.Error(err)
	}
	ioutil.ReadAll(resp.Body)

	keys := tr.IdleConnKeysForTesting()
	if e, g := 1, len(keys); e != g {
		t.Fatalf("After Get expected %d idle conn cache keys; got %d", e, g)
	}

	if e := "|http|" + ts.Listener.Addr().String(); keys[0] != e {
		t.Errorf("Expected idle cache key %q; got %q", e, keys[0])
	}

	tr.CloseIdleConnections()
	if e, g := 0, len(tr.IdleConnKeysForTesting()); e != g {
		t.Errorf("After CloseIdleConnections expected %d idle conn cache keys; got %d", e, g)
	}
}

// Tests that the HTTP transport re-uses connections when a client
// reads to the end of a response Body without closing it.
func TestTransportReadToEndReusesConn(t *testing.T) {
	defer afterTest(t)
	const msg = "foobar"

	var addrSeen map[string]int
	ts := th.NewServer(HandlerFunc(func(w ResponseWriter, r *Request) {
		addrSeen[r.RemoteAddr]++
		if r.URL.Path == "/chunked/" {
			w.WriteHeader(200)
			w.(Flusher).Flush()
		} else {
			w.Header().Set(ContentType, strconv.Itoa(len(msg)))
			w.WriteHeader(200)
		}
		w.Write([]byte(msg))
	}))
	defer ts.Close()

	buf := make([]byte, len(msg))

	for pi, path := range []string{"/content-length/", "/chunked/"} {
		wantLen := []int{len(msg), -1}[pi]
		addrSeen = make(map[string]int)
		for i := 0; i < 3; i++ {
			res, err := cli.Get(ts.URL + path)
			if err != nil {
				t.Errorf("Get %s: %v", path, err)
				continue
			}
			// We want to close this body eventually (before the
			// defer afterTest at top runs), but not before the
			// len(addrSeen) check at the bottom of this test,
			// since Closing this early in the loop would risk
			// making connections be re-used for the wrong reason.
			//TODO : @badu - it's a defer inside a loop
			defer res.CloseBody()

			if res.ContentLength != int64(wantLen) {
				t.Errorf("%s res.ContentLength = %d; want %d", path, res.ContentLength, wantLen)
			}
			n, err := res.Body.Read(buf)
			if n != len(msg) || err != io.EOF {
				t.Errorf("%s Read = %v, %v; want %d, EOF", path, n, err, len(msg))
			}
		}
		if len(addrSeen) != 1 {
			t.Errorf("for %s, server saw %d distinct client addresses; want 1", path, len(addrSeen))
		}
	}
}

func TestTransportMaxPerHostIdleConns(t *testing.T) {
	defer afterTest(t)
	resch := make(chan string)
	gotReq := make(chan bool)
	ts := th.NewServer(HandlerFunc(func(w ResponseWriter, r *Request) {
		gotReq <- true
		msg := <-resch
		_, err := w.Write([]byte(msg))
		if err != nil {
			t.Fatalf("Write: %v", err)
		}
	}))
	defer ts.Close()

	c := ts.Client()
	tr := c.Transport.(*Transport)
	maxIdleConnsPerHost := 2
	tr.MaxIdleConnsPerHost = maxIdleConnsPerHost

	// Start 3 outstanding requests and wait for the server to get them.
	// Their responses will hang until we write to resch, though.
	donech := make(chan bool)
	doReq := func() {
		resp, err := c.Get(ts.URL)
		if err != nil {
			t.Error(err)
			return
		}
		if _, err := ioutil.ReadAll(resp.Body); err != nil {
			t.Errorf("ReadAll: %v", err)
			return
		}
		donech <- true
	}
	go doReq()
	<-gotReq
	go doReq()
	<-gotReq
	go doReq()
	<-gotReq

	if e, g := 0, len(tr.IdleConnKeysForTesting()); e != g {
		t.Fatalf("Before writes, expected %d idle conn cache keys; got %d", e, g)
	}

	resch <- "res1"
	<-donech
	keys := tr.IdleConnKeysForTesting()
	if e, g := 1, len(keys); e != g {
		t.Fatalf("after first response, expected %d idle conn cache keys; got %d", e, g)
	}
	cacheKey := "|http|" + ts.Listener.Addr().String()
	if keys[0] != cacheKey {
		t.Fatalf("Expected idle cache key %q; got %q", cacheKey, keys[0])
	}
	if e, g := 1, tr.IdleConnCountForTesting(cacheKey); e != g {
		t.Errorf("after first response, expected %d idle conns; got %d", e, g)
	}

	resch <- "res2"
	<-donech
	if g, w := tr.IdleConnCountForTesting(cacheKey), 2; g != w {
		t.Errorf("after second response, idle conns = %d; want %d", g, w)
	}

	resch <- "res3"
	<-donech
	if g, w := tr.IdleConnCountForTesting(cacheKey), maxIdleConnsPerHost; g != w {
		t.Errorf("after third response, idle conns = %d; want %d", g, w)
	}
}

func TestTransportRemovesDeadIdleConnections(t *testing.T) {
	setParallel(t)
	defer afterTest(t)
	ts := th.NewServer(HandlerFunc(func(w ResponseWriter, r *Request) {
		io.WriteString(w, r.RemoteAddr)
	}))
	defer ts.Close()

	c := ts.Client()
	tr := c.Transport.(*Transport)

	doReq := func(name string) string {
		// Do a POST instead of a GET to prevent the Transport's
		// idempotent request retry logic from kicking in...
		res, err := c.Post(ts.URL, "", nil)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if res.StatusCode != 200 {
			t.Fatalf("%s: %v", name, res.Status)
		}
		defer res.CloseBody()
		slurp, err := ioutil.ReadAll(res.Body)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return string(slurp)
	}

	first := doReq("first")
	keys1 := tr.IdleConnKeysForTesting()

	ts.CloseClientConnections()

	var keys2 []string
	if !waitCondition(3*time.Second, 50*time.Millisecond, func() bool {
		keys2 = tr.IdleConnKeysForTesting()
		return len(keys2) == 0
	}) {
		t.Fatalf("Transport didn't notice idle connection's death.\nbefore: %q\n after: %q\n", keys1, keys2)
	}

	second := doReq("second")
	if first == second {
		t.Errorf("expected a different connection between requests. got %q both times", first)
	}
}

func TestTransportServerClosingUnexpectedly(t *testing.T) {
	setParallel(t)
	defer afterTest(t)
	ts := th.NewServer(hostPortHandler)
	defer ts.Close()
	c := ts.Client()

	fetch := func(n, retries int) string {
		condFatalf := func(format string, arg ...interface{}) {
			if retries <= 0 {
				t.Fatalf(format, arg...)
			}
			t.Logf("retrying shortly after expected error: "+format, arg...)
			time.Sleep(time.Second / time.Duration(retries))
		}
		for retries >= 0 {
			retries--
			res, err := c.Get(ts.URL)
			if err != nil {
				condFatalf("error in req #%d, GET: %v", n, err)
				continue
			}
			body, err := ioutil.ReadAll(res.Body)
			if err != nil {
				condFatalf("error in req #%d, ReadAll: %v", n, err)
				continue
			}
			res.CloseBody()
			return string(body)
		}
		panic("unreachable")
	}

	body1 := fetch(1, 0)
	body2 := fetch(2, 0)

	ts.CloseClientConnections() // surprise!

	// This test has an expected race. Sleeping for 25 ms prevents
	// it on most fast machines, causing the next fetch() call to
	// succeed quickly. But if we do get errors, fetch() will retry 5
	// times with some delays between.
	time.Sleep(25 * time.Millisecond)

	body3 := fetch(3, 5)

	if body1 != body2 {
		t.Errorf("expected body1 and body2 to be equal")
	}
	if body2 == body3 {
		t.Errorf("expected body2 and body3 to be different")
	}
}

// Test for https://golang.org/issue/2616 (appropriate issue number)
// This fails pretty reliably with GOMAXPROCS=100 or something high.
func TestStressSurpriseServerCloses(t *testing.T) {
	defer afterTest(t)
	if testing.Short() {
		t.Skip("skipping test in short mode")
	}
	ts := th.NewServer(HandlerFunc(func(w ResponseWriter, r *Request) {
		w.Header().Set(ContentLength, "5")
		w.Header().Set(ContentType, "text/plain")
		w.Write([]byte("Hello"))
		w.(Flusher).Flush()
		conn, buf, _ := w.(Hijacker).Hijack()
		buf.Flush()
		conn.Close()
	}))
	defer ts.Close()
	c := ts.Client()

	// Do a bunch of traffic from different goroutines. Send to activityc
	// after each request completes, regardless of whether it failed.
	// If these are too high, OS X exhausts its ephemeral ports
	// and hangs waiting for them to transition TCP states. That's
	// not what we want to test. TODO(bradfitz): use an io.Pipe
	// dialer for this test instead?
	const (
		numClients    = 20
		reqsPerClient = 25
	)
	activityc := make(chan bool)
	for i := 0; i < numClients; i++ {
		go func() {
			for i := 0; i < reqsPerClient; i++ {
				res, err := c.Get(ts.URL)
				if err == nil {
					// We expect errors since the server is
					// hanging up on us after telling us to
					// send more requests, so we don't
					// actually care what the error is.
					// But we want to close the body in cases
					// where we won the race.
					res.CloseBody()
				}
				activityc <- true
			}
		}()
	}

	// Make sure all the request come back, one way or another.
	for i := 0; i < numClients*reqsPerClient; i++ {
		select {
		case <-activityc:
		case <-time.After(5 * time.Second):
			t.Fatalf("presumed deadlock; no HTTP client activity seen in awhile")
		}
	}
}

// TestTransportHeadResponses verifies that we deal with Content-Lengths
// with no bodies properly
func TestTransportHeadResponses(t *testing.T) {
	defer afterTest(t)
	ts := th.NewServer(HandlerFunc(func(w ResponseWriter, r *Request) {
		if r.Method != HEAD {
			panic("expected HEAD; got " + r.Method)
		}
		w.Header().Set(ContentLength, "123")
		w.WriteHeader(200)
	}))
	defer ts.Close()
	c := ts.Client()

	for i := 0; i < 2; i++ {
		res, err := c.Head(ts.URL)
		if err != nil {
			t.Errorf("error on loop %d: %v", i, err)
			continue
		}
		if e, g := "123", res.Header.Get(ContentLength); e != g {
			t.Errorf("loop %d: expected Content-Length header of %q, got %q", i, e, g)
		}
		if e, g := int64(123), res.ContentLength; e != g {
			t.Errorf("loop %d: expected res.ContentLength of %v, got %v", i, e, g)
		}
		if all, err := ioutil.ReadAll(res.Body); err != nil {
			t.Errorf("loop %d: Body ReadAll: %v", i, err)
		} else if len(all) != 0 {
			t.Errorf("Bogus body %q", all)
		}
	}
}

// TestTransportHeadChunkedResponse verifies that we ignore chunked transfer-encoding
// on responses to HEAD requests.
func TestTransportHeadChunkedResponse(t *testing.T) {
	setParallel(t)
	defer afterTest(t)

	ts := th.NewServer(HandlerFunc(func(w ResponseWriter, r *Request) {
		if r.Method != HEAD {
			panic("expected HEAD; got " + r.Method)
		}
		w.Header().Set(TransferEncoding, DoChunked) // client should ignore
		w.Header().Set("x-client-ipport", r.RemoteAddr)
		w.WriteHeader(200)
	}))
	defer ts.Close()
	c := ts.Client()

	// Ensure that we wait for the readLoop to complete before
	// calling Head again
	didRead := make(chan bool, 2)

	// @comment : new way of waiting for server events
	handler := ListenTestEvent(ReadLoopBeforeNextReadEvent, func() {
		didRead <- true
	})
	defer handler.Kill()

	res1, err := c.Head(ts.URL)
	<-didRead
	if err != nil {
		t.Fatalf("request 1 error: %v", err)
	}

	res2, err := c.Head(ts.URL)
	<-didRead
	if err != nil {
		t.Fatalf("request 2 error: %v", err)
	}
	if v1, v2 := res1.Header.Get("x-client-ipport"), res2.Header.Get("x-client-ipport"); v1 != v2 {
		t.Errorf("ip/ports differed between head requests: %q vs %q", v1, v2)
	}
}

// Test that the modification made to the Request by the RoundTripper is cleaned up
func TestRoundTripGzip(t *testing.T) {
	setParallel(t)
	defer afterTest(t)
	const responseBody = "test response body"
	ts := th.NewServer(HandlerFunc(func(rw ResponseWriter, req *Request) {
		accept := req.Header.Get(AcceptEncoding)
		if expect := req.FormValue("expect_accept"); accept != expect {
			t.Errorf("in handler, test %v: Accept-Encoding = %q, want %q",
				req.FormValue("testnum"), accept, expect)
		}
		if accept == "gzip" {
			rw.Header().Set(ContentEncoding, "gzip")
			gz := gzip.NewWriter(rw)
			gz.Write([]byte(responseBody))
			gz.Close()
		} else {
			rw.Header().Set(ContentEncoding, accept)
			rw.Write([]byte(responseBody))
		}
	}))
	defer ts.Close()
	tr := ts.Client().Transport.(*Transport)

	for i, test := range roundTripTests {
		// Test basic request (no accept-encoding)
		req, _ := NewRequest(GET, fmt.Sprintf("%s/?testnum=%d&expect_accept=%s", ts.URL, i, test.expectAccept), nil)
		if test.accept != "" {
			req.Header.Set(AcceptEncoding, test.accept)
		}
		res, err := tr.RoundTrip(req)
		var body []byte
		if test.compressed {
			var r *gzip.Reader
			r, err = gzip.NewReader(res.Body)
			if err != nil {
				t.Errorf("%d. gzip NewHeaderReader: %v", i, err)
				continue
			}
			body, err = ioutil.ReadAll(r)
			res.CloseBody()
		} else {
			body, err = ioutil.ReadAll(res.Body)
		}
		if err != nil {
			t.Errorf("%d. Error: %q", i, err)
			continue
		}
		if g, e := string(body), responseBody; g != e {
			t.Errorf("%d. body = %q; want %q", i, g, e)
		}
		if g, e := req.Header.Get(AcceptEncoding), test.accept; g != e {
			t.Errorf("%d. Accept-Encoding = %q; want %q (it was mutated, in violation of RoundTrip contract)", i, g, e)
		}
		if g, e := res.Header.Get(ContentEncoding), test.accept; g != e {
			t.Errorf("%d. Content-Encoding = %q; want %q", i, g, e)
		}
	}

}

func TestTransportGzip(t *testing.T) {
	setParallel(t)
	defer afterTest(t)
	const testString = "The test string aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const nRandBytes = 1024 * 1024
	ts := th.NewServer(HandlerFunc(func(rw ResponseWriter, req *Request) {
		if req.Method == HEAD {
			if g := req.Header.Get(AcceptEncoding); g != "" {
				t.Errorf("HEAD request sent with Accept-Encoding of %q; want none", g)
			}
			return
		}
		if g, e := req.Header.Get(AcceptEncoding), "gzip"; g != e {
			t.Errorf("Accept-Encoding = %q, want %q", g, e)
		}
		rw.Header().Set(ContentEncoding, "gzip")

		var w io.Writer = rw
		var buf bytes.Buffer
		if req.FormValue(DoChunked) == "0" {
			w = &buf
			defer io.Copy(rw, &buf)
			defer func() {
				rw.Header().Set(ContentLength, strconv.Itoa(buf.Len()))
			}()
		}
		gz := gzip.NewWriter(w)
		gz.Write([]byte(testString))
		if req.FormValue("body") == "large" {
			io.CopyN(gz, rand.Reader, nRandBytes)
		}
		gz.Close()
	}))
	defer ts.Close()
	c := ts.Client()

	for _, chunked := range []string{"1", "0"} {
		// First fetch something large, but only read some of it.
		res, err := c.Get(ts.URL + "/?body=large&chunked=" + chunked)
		if err != nil {
			t.Fatalf("large get: %v", err)
		}
		buf := make([]byte, len(testString))
		n, err := io.ReadFull(res.Body, buf)
		if err != nil {
			t.Fatalf("partial read of large response: size=%d, %v", n, err)
		}
		if e, g := testString, string(buf); e != g {
			t.Errorf("partial read got %q, expected %q", g, e)
		}
		res.CloseBody()
		// Read on the body, even though it's closed
		n, err = res.Body.Read(buf)
		if n != 0 || err == nil {
			t.Errorf("expected error post-closed large Read; got = %d, %v", n, err)
		}

		// Then something small.
		res, err = c.Get(ts.URL + "/?chunked=" + chunked)
		if err != nil {
			t.Fatal(err)
		}
		body, err := ioutil.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		if g, e := string(body), testString; g != e {
			t.Fatalf("body = %q; want %q", g, e)
		}
		if g, e := res.Header.Get(ContentEncoding), ""; g != e {
			t.Fatalf("Content-Encoding = %q; want %q", g, e)
		}

		// Read on the body after it's been fully read:
		n, err = res.Body.Read(buf)
		if n != 0 || err == nil {
			t.Errorf("expected Read error after exhausted reads; got %d, %v", n, err)
		}
		res.CloseBody()
		n, err = res.Body.Read(buf)
		if n != 0 || err == nil {
			t.Errorf("expected Read error after Close; got %d, %v", n, err)
		}
	}

	// And a HEAD request too, because they're always weird.
	res, err := c.Head(ts.URL)
	if err != nil {
		t.Fatalf("Head: %v", err)
	}
	if res.StatusCode != 200 {
		t.Errorf("Head status=%d; want=200", res.StatusCode)
	}
}

// If a request has Expect:100-continue header, the request blocks sending body until the first response.
// Premature consumption of the request body should not be occurred.
func TestTransportExpect100Continue(t *testing.T) {
	setParallel(t)
	defer afterTest(t)

	ts := th.NewServer(HandlerFunc(func(rw ResponseWriter, req *Request) {
		switch req.URL.Path {
		case "/100":
			// This endpoint implicitly responds 100 Continue and reads body.
			if _, err := io.Copy(ioutil.Discard, req.Body); err != nil {
				t.Error("Failed to read Body", err)
			}
			rw.WriteHeader(StatusOK)
		case "/200":
			// Go 1.5 adds Connection: close header if the client expect
			// continue but not entire request body is consumed.
			rw.WriteHeader(StatusOK)
		case "/500":
			rw.WriteHeader(StatusInternalServerError)
		case "/keepalive":
			// This hijacked endpoint responds error without Connection:close.
			_, bufrw, err := rw.(Hijacker).Hijack()
			if err != nil {
				log.Fatal(err)
			}
			bufrw.WriteString("HTTP/1.1 500 Internal Server Error\r\n")
			bufrw.WriteString("Content-Length: 0\r\n\r\n")
			bufrw.Flush()
		case "/timeout":
			// This endpoint tries to read body without 100 (Continue) response.
			// After ExpectContinueTimeout, the reading will be started.
			conn, bufrw, err := rw.(Hijacker).Hijack()
			if err != nil {
				log.Fatal(err)
			}
			if _, err := io.CopyN(ioutil.Discard, bufrw, req.ContentLength); err != nil {
				t.Error("Failed to read Body", err)
			}
			bufrw.WriteString("HTTP/1.1 200 OK\r\n\r\n")
			bufrw.Flush()
			conn.Close()
		}

	}))
	defer ts.Close()

	tests := []struct {
		path   string
		body   []byte
		sent   int
		status int
	}{
		{path: "/100", body: []byte("hello"), sent: 5, status: 200},       // Got 100 followed by 200, entire body is sent.
		{path: "/200", body: []byte("hello"), sent: 0, status: 200},       // Got 200 without 100. body isn't sent.
		{path: "/500", body: []byte("hello"), sent: 0, status: 500},       // Got 500 without 100. body isn't sent.
		{path: "/keepalive", body: []byte("hello"), sent: 0, status: 500}, // Although without Connection:close, body isn't sent.
		{path: "/timeout", body: []byte("hello"), sent: 5, status: 200},   // Timeout exceeded and entire body is sent.
	}

	c := ts.Client()
	for i, v := range tests {
		tr := &Transport{
			ExpectContinueTimeout: 2 * time.Second,
		}
		//TODO : @badu - it's a defer inside a loop
		defer tr.CloseIdleConnections()
		c.Transport = tr
		body := bytes.NewReader(v.body)
		req, err := NewRequest(PUT, ts.URL+v.path, body)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set(Expect, "100-continue")
		req.ContentLength = int64(len(v.body))

		resp, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.CloseBody()

		sent := len(v.body) - body.Len()
		if v.status != resp.StatusCode {
			t.Errorf("test %d: status code should be %d but got %d. (%s)", i, v.status, resp.StatusCode, v.path)
		}
		if v.sent != sent {
			t.Errorf("test %d: sent body should be %d but sent %d. (%s)", i, v.sent, sent, v.path)
		}
	}
}

func TestSocks5Proxy(t *testing.T) {
	defer afterTest(t)
	ch := make(chan string, 1)
	ts := th.NewServer(HandlerFunc(func(w ResponseWriter, r *Request) {
		ch <- "real server"
	}))
	defer ts.Close()
	l := newLocalListener(t)
	defer l.Close()
	go func() {
		defer close(ch)
		s, err := l.Accept()
		if err != nil {
			t.Errorf("socks5 proxy Accept(): %v", err)
			return
		}
		defer s.Close()
		var buf [22]byte
		if _, err := io.ReadFull(s, buf[:3]); err != nil {
			t.Errorf("socks5 proxy initial read: %v", err)
			return
		}
		if want := []byte{5, 1, 0}; !bytes.Equal(buf[:3], want) {
			t.Errorf("socks5 proxy initial read: got %v, want %v", buf[:3], want)
			return
		}
		if _, err := s.Write([]byte{5, 0}); err != nil {
			t.Errorf("socks5 proxy initial write: %v", err)
			return
		}
		if _, err := io.ReadFull(s, buf[:4]); err != nil {
			t.Errorf("socks5 proxy second read: %v", err)
			return
		}
		if want := []byte{5, 1, 0}; !bytes.Equal(buf[:3], want) {
			t.Errorf("socks5 proxy second read: got %v, want %v", buf[:3], want)
			return
		}
		var ipLen int
		switch buf[3] {
		case 1:
			ipLen = 4
		case 4:
			ipLen = 16
		default:
			t.Fatalf("socks5 proxy second read: unexpected address type %v", buf[4])
		}
		if _, err := io.ReadFull(s, buf[4:ipLen+6]); err != nil {
			t.Errorf("socks5 proxy address read: %v", err)
			return
		}
		ip := net.IP(buf[4 : ipLen+4])
		port := binary.BigEndian.Uint16(buf[ipLen+4 : ipLen+6])
		copy(buf[:3], []byte{5, 0, 0})
		if _, err := s.Write(buf[:ipLen+6]); err != nil {
			t.Errorf("socks5 proxy connect write: %v", err)
			return
		}
		done := make(chan struct{})
		srv := &Server{Handler: HandlerFunc(func(w ResponseWriter, r *Request) {
			done <- struct{}{}
		})}
		srv.Serve(&oneConnListener{conn: s})
		<-done
		srv.Shutdown(context.Background())
		ch <- fmt.Sprintf("proxy for %s:%d", ip, port)
	}()

	pu, err := url.Parse("socks5://" + l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	c := ts.Client()
	c.Transport.(*Transport).Proxy = ProxyURL(pu)
	if _, err := c.Head(ts.URL); err != nil {
		t.Error(err)
	}
	var got string
	select {
	case got = <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout connecting to socks5 proxy")
	}
	tsu, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	want := "proxy for " + tsu.Host
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestTransportProxy(t *testing.T) {
	defer afterTest(t)
	ch := make(chan string, 1)
	ts := th.NewServer(HandlerFunc(func(w ResponseWriter, r *Request) {
		ch <- "real server"
	}))
	defer ts.Close()
	proxy := th.NewServer(HandlerFunc(func(w ResponseWriter, r *Request) {
		ch <- "proxy for " + r.URL.String()
	}))
	defer proxy.Close()

	pu, err := url.Parse(proxy.URL)
	if err != nil {
		t.Fatal(err)
	}
	c := ts.Client()
	c.Transport.(*Transport).Proxy = ProxyURL(pu)
	if _, err := c.Head(ts.URL); err != nil {
		t.Error(err)
	}
	var got string
	select {
	case got = <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout connecting to http proxy")
	}
	want := "proxy for " + ts.URL + "/"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// Issue 16997: test transport dial preserves typed errors
func TestTransportDialPreservesNetOpProxyError(t *testing.T) {
	defer afterTest(t)

	var errDial = errors.New("some dial error")

	tr := &Transport{
		Proxy: func(*Request) (*url.URL, error) {
			return url.Parse("http://proxy.fake.tld/")
		},
		DialContext: func(context.Context, string, string) (net.Conn, error) {
			return nil, errDial
		},
	}
	defer tr.CloseIdleConnections()

	c := &cli.Client{Transport: tr}
	req, _ := NewRequest(GET, "http://fake.tld", nil)
	res, err := c.Do(req)
	if err == nil {
		res.CloseBody()
		t.Fatal("wanted a non-nil error")
	}

	uerr, ok := err.(*url.Error)
	if !ok {
		t.Fatalf("got %T, want *url.Error", err)
	}
	oe, ok := uerr.Err.(*net.OpError)
	if !ok {
		t.Fatalf("url.Error.Err =  %T; want *net.OpError", uerr.Err)
	}
	want := &net.OpError{
		Op:  "proxyconnect",
		Net: "tcp",
		Err: errDial, // original error, unwrapped.
	}
	if !reflect.DeepEqual(oe, want) {
		t.Errorf("Got error %#v; want %#v", oe, want)
	}
}

// TestTransportGzipRecursive sends a gzip quine and checks that the
// client gets the same value back. This is more cute than anything,
// but checks that we don't recurse forever, and checks that
// Content-Encoding is removed.
func TestTransportGzipRecursive(t *testing.T) {
	defer afterTest(t)
	ts := th.NewServer(HandlerFunc(func(w ResponseWriter, r *Request) {
		w.Header().Set(ContentEncoding, "gzip")
		w.Write(rgz)
	}))
	defer ts.Close()

	c := ts.Client()
	res, err := c.Get(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	body, err := ioutil.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(body, rgz) {
		t.Fatalf("Incorrect result from recursive gz:\nhave=%x\nwant=%x",
			body, rgz)
	}
	if g, e := res.Header.Get(ContentEncoding), ""; g != e {
		t.Fatalf("Content-Encoding = %q; want %q", g, e)
	}
}

// golang.org/issue/7750: request fails when server replies with
// a short gzip body
func TestTransportGzipShort(t *testing.T) {
	defer afterTest(t)
	ts := th.NewServer(HandlerFunc(func(w ResponseWriter, r *Request) {
		w.Header().Set(ContentEncoding, "gzip")
		w.Write([]byte{0x1f, 0x8b})
	}))
	defer ts.Close()

	c := ts.Client()
	res, err := c.Get(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer res.CloseBody()
	_, err = ioutil.ReadAll(res.Body)
	if err == nil {
		t.Fatal("Expect an error from reading a body.")
	}
	if err != io.ErrUnexpectedEOF {
		t.Errorf("ReadAll error = %v; want io.ErrUnexpectedEOF", err)
	}
}

// tests that persistent goroutine connections shut down when no longer desired.
func TestTransportPersistConnLeak(t *testing.T) {
	// Not parallel: counts goroutines
	defer afterTest(t)

	const numReq = 25
	gotReqCh := make(chan bool, numReq)
	unblockCh := make(chan bool, numReq)
	ts := th.NewServer(HandlerFunc(func(w ResponseWriter, r *Request) {
		gotReqCh <- true
		<-unblockCh
		w.Header().Set(ContentLength, "0")
		w.WriteHeader(204)
	}))
	defer ts.Close()
	c := ts.Client()
	tr := c.Transport.(*Transport)

	n0 := runtime.NumGoroutine()

	didReqCh := make(chan bool, numReq)
	failed := make(chan bool, numReq)
	for i := 0; i < numReq; i++ {
		go func() {
			res, err := c.Get(ts.URL)
			didReqCh <- true
			if err != nil {
				t.Errorf("client fetch error: %v", err)
				failed <- true
				return
			}
			res.CloseBody()
		}()
	}

	// Wait for all goroutines to be stuck in the Handler.
	for i := 0; i < numReq; i++ {
		select {
		case <-gotReqCh:
			// ok
		case <-failed:
			close(unblockCh)
			return
		}
	}

	nhigh := runtime.NumGoroutine()

	// Tell all handlers to unblock and reply.
	for i := 0; i < numReq; i++ {
		unblockCh <- true
	}

	// Wait for all HTTP clients to be done.
	for i := 0; i < numReq; i++ {
		<-didReqCh
	}

	tr.CloseIdleConnections()
	nfinal := waitNumGoroutine(n0 + 5)

	growth := nfinal - n0

	// We expect 0 or 1 extra goroutine, empirically. Allow up to 5.
	// Previously we were leaking one per numReq.
	if int(growth) > 5 {
		t.Logf("goroutine growth: %d -> %d -> %d (delta: %d)", n0, nhigh, nfinal, growth)
		t.Error("too many new goroutines")
	}
}

// golang.org/issue/4531: Transport leaks goroutines when
// request.ContentLength is explicitly short
func TestTransportPersistConnLeakShortBody(t *testing.T) {
	// Not parallel: measures goroutines.
	defer afterTest(t)
	ts := th.NewServer(HandlerFunc(func(w ResponseWriter, r *Request) {
	}))
	defer ts.Close()
	c := ts.Client()
	tr := c.Transport.(*Transport)

	n0 := runtime.NumGoroutine()
	body := []byte("Hello")
	for i := 0; i < 20; i++ {
		req, err := NewRequest(POST, ts.URL, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.ContentLength = int64(len(body) - 2) // explicitly short
		_, err = c.Do(req)
		if err == nil {
			t.Fatal("Expect an error from writing too long of a body.")
		}
	}
	nhigh := runtime.NumGoroutine()
	tr.CloseIdleConnections()
	nfinal := waitNumGoroutine(n0 + 5)

	growth := nfinal - n0

	// We expect 0 or 1 extra goroutine, empirically. Allow up to 5.
	// Previously we were leaking one per numReq.
	t.Logf("goroutine growth: %d -> %d -> %d (delta: %d)", n0, nhigh, nfinal, growth)
	if int(growth) > 5 {
		t.Error("too many new goroutines")
	}
}

// This used to crash; https://golang.org/issue/3266
func TestTransportIdleConnCrash(t *testing.T) {
	defer afterTest(t)
	var tr *Transport

	unblockCh := make(chan bool, 1)
	ts := th.NewServer(HandlerFunc(func(w ResponseWriter, r *Request) {
		<-unblockCh
		tr.CloseIdleConnections()
	}))
	defer ts.Close()
	c := ts.Client()
	tr = c.Transport.(*Transport)

	didreq := make(chan bool)
	go func() {
		res, err := c.Get(ts.URL)
		if err != nil {
			t.Error(err)
		} else {
			res.CloseBody() // returns idle conn
		}
		didreq <- true
	}()
	unblockCh <- true
	<-didreq
}

// Test that the transport doesn't close the TCP connection early,
// before the response body has been read. This was a regression
// which sadly lacked a triggering test. The large response body made
// the old race easier to trigger.
func TestIssue3644(t *testing.T) {
	defer afterTest(t)
	const numFoos = 5000
	ts := th.NewServer(HandlerFunc(func(w ResponseWriter, r *Request) {
		w.Header().Set(Connection, DoClose)
		for i := 0; i < numFoos; i++ {
			w.Write([]byte("foo "))
		}
	}))
	defer ts.Close()
	c := ts.Client()
	res, err := c.Get(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer res.CloseBody()
	bs, err := ioutil.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if len(bs) != numFoos*len("foo ") {
		t.Errorf("unexpected response length")
	}
}

// Test that a client receives a server's reply, even if the server doesn't read
// the entire request body.
func TestIssue3595(t *testing.T) {
	setParallel(t)
	defer afterTest(t)
	const deniedMsg = "sorry, denied."
	ts := th.NewServer(HandlerFunc(func(w ResponseWriter, r *Request) {
		Error(w, deniedMsg, StatusUnauthorized)
	}))
	defer ts.Close()
	c := ts.Client()
	res, err := c.Post(ts.URL, OctetStream, neverEnding('a'))
	if err != nil {
		t.Errorf("Post: %v", err)
		return
	}
	got, err := ioutil.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("Body ReadAll: %v", err)
	}
	if !strings.Contains(string(got), deniedMsg) {
		t.Errorf("Known bug: response %q does not contain %q", got, deniedMsg)
	}
}

// From https://golang.org/issue/4454 ,
// "client fails to handle requests with no body and chunked encoding"
func TestChunkedNoContent(t *testing.T) {
	defer afterTest(t)
	ts := th.NewServer(HandlerFunc(func(w ResponseWriter, r *Request) {
		w.WriteHeader(StatusNoContent)
	}))
	defer ts.Close()

	c := ts.Client()
	for _, closeBody := range []bool{true, false} {
		const n = 4
		for i := 1; i <= n; i++ {
			res, err := c.Get(ts.URL)
			if err != nil {
				t.Errorf("closingBody=%v, req %d/%d: %v", closeBody, i, n, err)
			} else {
				if closeBody {
					res.CloseBody()
				}
			}
		}
	}
}

func TestTransportConcurrency(t *testing.T) {
	// @comment : deprecated comment - Not parallel: uses global test hooks.
	setParallel(t)
	defer afterTest(t)
	maxProcs, numReqs := 16, 500
	if testing.Short() {
		maxProcs, numReqs = 4, 50
	}
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(maxProcs))
	ts := th.NewServer(HandlerFunc(func(w ResponseWriter, r *Request) {
		fmt.Fprintf(w, "%v", r.FormValue("echo"))
	}))
	defer ts.Close()

	var wg sync.WaitGroup
	wg.Add(numReqs)

	// @comment : new way of waiting for server events
	eventHandler := ListenTestEvent(PrePendingDialEvent, func() {
		wg.Add(1)
	})
	eventHandler.Kill()
	eventHandler2 := ListenTestEvent(PrePendingDialEvent, func() {
		wg.Done()
	})
	eventHandler2.Kill()

	c := ts.Client()
	reqs := make(chan string)
	defer close(reqs)

	for i := 0; i < maxProcs*2; i++ {
		go func() {
			for req := range reqs {
				res, err := c.Get(ts.URL + "/?echo=" + req)
				if err != nil {
					t.Errorf("error on req %s: %v", req, err)
					wg.Done()
					continue
				}
				all, err := ioutil.ReadAll(res.Body)
				if err != nil {
					t.Errorf("read error on req %s: %v", req, err)
					wg.Done()
					continue
				}
				if string(all) != req {
					t.Errorf("body of req %s = %q; want %q", req, all, req)
				}
				res.CloseBody()
				wg.Done()
			}
		}()
	}
	for i := 0; i < numReqs; i++ {
		reqs <- fmt.Sprintf("request-%d", i)
	}
	wg.Wait()
}

func TestIssue4191InfiniteGetTimeout(t *testing.T) {
	setParallel(t)
	defer afterTest(t)
	const debug = false
	srvMx := mux.NewServeMux()
	srvMx.HandleFunc("/get", func(w ResponseWriter, r *Request) {
		io.Copy(w, neverEnding('a'))
	})
	ts := th.NewServer(srvMx)
	defer ts.Close()
	timeout := 100 * time.Millisecond

	c := ts.Client()
	c.Transport.(*Transport).DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		conn, err := net.Dial(network, addr)
		if err != nil {
			return nil, err
		}
		conn.SetDeadline(time.Now().Add(timeout))
		if debug {
			conn = NewLoggingConn("client", conn)
		}
		return conn, nil
	}

	getFailed := false
	nRuns := 5
	if testing.Short() {
		nRuns = 1
	}
	for i := 0; i < nRuns; i++ {
		if debug {
			println("run", i+1, "of", nRuns)
		}
		sres, err := c.Get(ts.URL + "/get")
		if err != nil {
			if !getFailed {
				// Make the timeout longer, once.
				getFailed = true
				t.Logf("increasing timeout")
				i--
				timeout *= 10
				continue
			}
			t.Errorf("Error issuing GET: %v", err)
			break
		}
		_, err = io.Copy(ioutil.Discard, sres.Body)
		if err == nil {
			t.Errorf("Unexpected successful copy")
			break
		}
	}
	if debug {
		println("tests complete; waiting for handlers to finish")
	}
}

func TestIssue4191InfiniteGetToPutTimeout(t *testing.T) {
	setParallel(t)
	defer afterTest(t)
	const debug = false
	srvMx := mux.NewServeMux()
	srvMx.HandleFunc("/get", func(w ResponseWriter, r *Request) {
		io.Copy(w, neverEnding('a'))
	})
	srvMx.HandleFunc("/put", func(w ResponseWriter, r *Request) {
		defer r.CloseBody()
		io.Copy(ioutil.Discard, r.Body)
	})
	ts := th.NewServer(srvMx)
	timeout := 100 * time.Millisecond

	c := ts.Client()
	c.Transport.(*Transport).DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		conn, err := net.Dial(network, addr)
		if err != nil {
			return nil, err
		}
		conn.SetDeadline(time.Now().Add(timeout))
		if debug {
			conn = NewLoggingConn("client", conn)
		}
		return conn, nil
	}

	getFailed := false
	nRuns := 5
	if testing.Short() {
		nRuns = 1
	}
	for i := 0; i < nRuns; i++ {
		if debug {
			println("run", i+1, "of", nRuns)
		}
		sres, err := c.Get(ts.URL + "/get")
		if err != nil {
			if !getFailed {
				// Make the timeout longer, once.
				getFailed = true
				t.Logf("increasing timeout")
				i--
				timeout *= 10
				continue
			}
			t.Errorf("Error issuing GET: %v", err)
			break
		}
		req, _ := NewRequest(PUT, ts.URL+"/put", sres.Body)
		_, err = c.Do(req)
		if err == nil {
			sres.CloseBody()
			t.Errorf("Unexpected successful PUT")
			break
		}
		sres.CloseBody()
	}
	if debug {
		println("tests complete; waiting for handlers to finish")
	}
	ts.Close()
}

func TestTransportResponseHeaderTimeout(t *testing.T) {
	setParallel(t)
	defer afterTest(t)
	if testing.Short() {
		t.Skip("skipping timeout test in -short mode")
	}
	inHandler := make(chan bool, 1)
	srvMx := mux.NewServeMux()
	srvMx.HandleFunc("/fast", func(w ResponseWriter, r *Request) {
		inHandler <- true
	})
	srvMx.HandleFunc("/slow", func(w ResponseWriter, r *Request) {
		inHandler <- true
		time.Sleep(2 * time.Second)
	})
	ts := th.NewServer(srvMx)
	defer ts.Close()

	c := ts.Client()
	c.Transport.(*Transport).ResponseHeaderTimeout = 500 * time.Millisecond

	tests := []struct {
		path    string
		want    int
		wantErr string
	}{
		{path: "/fast", want: 200},
		{path: "/slow", wantErr: "timeout awaiting response headers"},
		{path: "/fast", want: 200},
	}
	for i, tt := range tests {
		req, _ := NewRequest(GET, ts.URL+tt.path, nil)
		req = req.WithContext(context.WithValue(req.Context(), TLogKey{}, t.Logf))
		res, err := c.Do(req)
		select {
		case <-inHandler:
		case <-time.After(5 * time.Second):
			t.Errorf("never entered handler for test index %d, %s", i, tt.path)
			continue
		}
		if err != nil {
			uerr, ok := err.(*url.Error)
			if !ok {
				t.Errorf("error is not an url.Error; got: %#v", err)
				continue
			}
			// TODO [COMMENT] : @badu - net.Error is an interface : Timeout() bool and Temporary() bool
			nerr, ok := uerr.Err.(net.Error)
			if !ok {
				t.Errorf("error does not satisfy net.Error interface; got: %#v", err)
				continue
			}
			if !nerr.Timeout() {
				t.Errorf("want timeout error; got: %q", nerr)
				continue
			}
			if strings.Contains(err.Error(), tt.wantErr) {
				continue
			}
			t.Errorf("%d. unexpected error: %v", i, err)
			continue
		}
		if tt.wantErr != "" {
			t.Errorf("%d. no error. expected error: %v", i, tt.wantErr)
			continue
		}
		if res.StatusCode != tt.want {
			t.Errorf("%d for path %q status = %d; want %d", i, tt.path, res.StatusCode, tt.want)
		}
	}
}

func TestCancelRequestWithChannelBeforeDo(t *testing.T) {
	setParallel(t)
	defer afterTest(t)
	unblockc := make(chan bool)
	ts := th.NewServer(HandlerFunc(func(w ResponseWriter, r *Request) {
		<-unblockc
	}))
	defer ts.Close()
	defer close(unblockc)

	c := ts.Client()

	req, _ := NewRequest(GET, ts.URL, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req = req.WithContext(ctx)

	_, err := c.Do(req)
	if ue, ok := err.(*url.Error); ok {
		err = ue.Err
	}
	if err != context.Canceled {
		t.Errorf("Do error = %v; want %v", err, context.Canceled)
	}
}

// golang.org/issue/3672 -- Client can't close HTTP stream
// Calling Close on a Response.Body used to just read until EOF.
// Now it actually closes the TCP connection.
func TestTransportCloseResponseBody(t *testing.T) {
	defer afterTest(t)
	writeErr := make(chan error, 1)
	msg := []byte("young\n")
	ts := th.NewServer(HandlerFunc(func(w ResponseWriter, r *Request) {
		for {
			_, err := w.Write(msg)
			if err != nil {
				writeErr <- err
				return
			}
			w.(Flusher).Flush()
		}
	}))
	defer ts.Close()

	c := ts.Client()
	//tr := c.Transport.(*Transport)

	req, _ := NewRequest(GET, ts.URL, nil)
	//defer tr.CancelRequest(req)

	res, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}

	const repeats = 3
	buf := make([]byte, len(msg)*repeats)
	want := bytes.Repeat(msg, repeats)

	_, err = io.ReadFull(res.Body, buf)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(buf, want) {
		t.Fatalf("read %q; want %q", buf, want)
	}
	didClose := make(chan error, 1)
	go func() {
		didClose <- res.Body.Close()
	}()
	select {
	case err := <-didClose:
		if err != nil {
			t.Errorf("Close = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("too long waiting for close")
	}
	select {
	case err := <-writeErr:
		if err == nil {
			t.Errorf("expected non-nil write error")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("too long waiting for write error")
	}
}

func TestTransportAltProto(t *testing.T) {
	defer afterTest(t)
	tr := &Transport{}
	c := &cli.Client{Transport: tr}
	tr.RegisterProtocol("foo", fooProto{})
	res, err := c.Get("foo://bar.com/path")
	if err != nil {
		t.Fatal(err)
	}
	bodyb, err := ioutil.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	body := string(bodyb)
	if e := "You wanted foo://bar.com/path"; body != e {
		t.Errorf("got response %q, want %q", body, e)
	}
}

func TestTransportNoHost(t *testing.T) {
	defer afterTest(t)
	tr := &Transport{}
	_, err := tr.RoundTrip(&Request{
		Header: make(Header),
		URL: &url.URL{
			Scheme: HTTP,
		},
	})
	want := "http: no Host in request URL"
	if got := fmt.Sprint(err); got != want {
		t.Errorf("error = %v; want %q", err, want)
	}
}

// Issue 13311
func TestTransportEmptyMethod(t *testing.T) {
	req, _ := NewRequest(GET, "http://foo.com/", nil)
	req.Method = ""                             // docs say "For client requests an empty string means GET"
	got, err := util.DumpRequestOut(req, false) // DumpRequestOut uses Transport
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "GET ") {
		t.Fatalf("expected substring 'GET '; got: %s", got)
	}
}

func TestTransportSocketLateBinding(t *testing.T) {
	setParallel(t)
	defer afterTest(t)

	srvMx := mux.NewServeMux()
	fooGate := make(chan bool, 1)
	srvMx.HandleFunc("/foo", func(w ResponseWriter, r *Request) {
		w.Header().Set("foo-ipport", r.RemoteAddr)
		w.(Flusher).Flush()
		<-fooGate
	})
	srvMx.HandleFunc("/bar", func(w ResponseWriter, r *Request) {
		w.Header().Set("bar-ipport", r.RemoteAddr)
	})
	ts := th.NewServer(srvMx)
	defer ts.Close()

	dialGate := make(chan bool, 1)
	c := ts.Client()
	c.Transport.(*Transport).DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		if <-dialGate {
			return net.Dial(network, addr)
		}
		return nil, errors.New("manually closed")
	}

	dialGate <- true // only allow one dial
	fooRes, err := c.Get(ts.URL + "/foo")
	if err != nil {
		t.Fatal(err)
	}
	fooAddr := fooRes.Header.Get("foo-ipport")
	if fooAddr == "" {
		t.Fatal("No addr on /foo request")
	}
	time.AfterFunc(200*time.Millisecond, func() {
		// let the foo response finish so we can use its
		// connection for /bar
		fooGate <- true
		io.Copy(ioutil.Discard, fooRes.Body)
		fooRes.CloseBody()
	})

	barRes, err := c.Get(ts.URL + "/bar")
	if err != nil {
		t.Fatal(err)
	}
	barAddr := barRes.Header.Get("bar-ipport")
	if barAddr != fooAddr {
		t.Fatalf("/foo came from conn %q; /bar came from %q instead", fooAddr, barAddr)
	}
	barRes.CloseBody()
	dialGate <- false
}

// Issue 2184
func TestTransportReading100Continue(t *testing.T) {
	defer afterTest(t)

	const numReqs = 5
	reqBody := func(n int) string { return fmt.Sprintf("request body %d", n) }
	reqID := func(n int) string { return fmt.Sprintf("REQ-ID-%d", n) }

	send100Response := func(w *io.PipeWriter, r *io.PipeReader) {
		defer w.Close()
		defer r.Close()
		br := bufio.NewReader(r)
		n := 0
		for {
			n++
			req, err := ReadRequest(br)
			if err == io.EOF {
				return
			}
			if err != nil {
				t.Error(err)
				return
			}
			slurp, err := ioutil.ReadAll(req.Body)
			if err != nil {
				t.Errorf("Server request body slurp: %v", err)
				return
			}
			id := req.Header.Get("Request-Id")
			resCode := req.Header.Get("X-Want-Response-Code")
			if resCode == "" {
				resCode = "100 Continue"
				if string(slurp) != reqBody(n) {
					t.Errorf("Server got %q, %v; want %q", slurp, err, reqBody(n))
				}
			}
			body := fmt.Sprintf("Response number %d", n)
			v := []byte(strings.Replace(fmt.Sprintf(`HTTP/1.1 %s
Date: Thu, 28 Feb 2013 17:55:41 GMT

HTTP/1.1 200 OK
Content-Type: text/html
Echo-Request-Id: %s
Content-Length: %d

%s`, resCode, id, len(body), body), "\n", "\r\n", -1))
			w.Write(v)
			if id == reqID(numReqs) {
				return
			}
		}

	}

	tr := &Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			sr, sw := io.Pipe() // server read/write
			cr, cw := io.Pipe() // client read/write
			conn := &rwTestConn{
				Reader: cr,
				Writer: sw,
				closeFunc: func() error {
					sw.Close()
					cw.Close()
					return nil
				},
			}
			go send100Response(cw, sr)
			return conn, nil
		},
		DisableKeepAlives: false,
	}
	defer tr.CloseIdleConnections()
	c := &cli.Client{Transport: tr}

	testResponse := func(req *Request, name string, wantCode int) {
		res, err := c.Do(req)
		if err != nil {
			t.Fatalf("%s: Do: %v", name, err)
		}
		if res.StatusCode != wantCode {
			t.Fatalf("%s: Response Statuscode=%d; want %d", name, res.StatusCode, wantCode)
		}
		if id, idBack := req.Header.Get("Request-Id"), res.Header.Get("Echo-Request-Id"); id != "" && id != idBack {
			t.Errorf("%s: response id %q != request id %q", name, idBack, id)
		}
		_, err = ioutil.ReadAll(res.Body)
		if err != nil {
			t.Fatalf("%s: Slurp error: %v", name, err)
		}
	}

	// Few 100 responses, making sure we're not off-by-one.
	for i := 1; i <= numReqs; i++ {
		req, _ := NewRequest(POST, "http://dummy.tld/", strings.NewReader(reqBody(i)))
		req.Header.Set("Request-Id", reqID(i))
		testResponse(req, fmt.Sprintf("100, %d/%d", i, numReqs), 200)
	}

	// And some other informational 1xx but non-100 responses, to test
	// we return them but don't re-use the connection.
	for i := 1; i <= numReqs; i++ {
		req, _ := NewRequest(POST, "http://other.tld/", strings.NewReader(reqBody(i)))
		req.Header.Set("X-Want-Response-Code", "123 Sesame Street")
		testResponse(req, fmt.Sprintf("123, %d/%d", i, numReqs), 123)
	}
}

func TestProxyFromEnvironment(t *testing.T) {
	ResetProxyEnv()
	defer ResetProxyEnv()
	var proxyFromEnvTests = []proxyFromEnvTest{
		{env: "127.0.0.1:8080", want: "http://127.0.0.1:8080"},
		{env: "cache.corp.example.com:1234", want: "http://cache.corp.example.com:1234"},
		{env: "cache.corp.example.com", want: "http://cache.corp.example.com"},
		{env: "https://cache.corp.example.com", want: "https://cache.corp.example.com"},
		{env: "http://127.0.0.1:8080", want: "http://127.0.0.1:8080"},
		{env: "https://127.0.0.1:8080", want: "https://127.0.0.1:8080"},
		{env: "socks5://127.0.0.1", want: "socks5://127.0.0.1"},

		// Don't use secure for http
		{req: "http://insecure.tld/", env: "http.proxy.tld", httpsenv: "secure.proxy.tld", want: "http://http.proxy.tld"},
		// Use secure for https.
		{req: "https://secure.tld/", env: "http.proxy.tld", httpsenv: "secure.proxy.tld", want: "http://secure.proxy.tld"},
		{req: "https://secure.tld/", env: "http.proxy.tld", httpsenv: "https://secure.proxy.tld", want: "https://secure.proxy.tld"},

		// Issue 16405: don't use HTTP_PROXY in a CGI environment,
		// where HTTP_PROXY can be attacker-controlled.
		{env: "http://10.1.2.3:8080", reqmeth: POST,
			want:    "<nil>",
			wanterr: errors.New("net/http: refusing to use HTTP_PROXY value in CGI environment; see golang.org/s/cgihttpproxy")},

		{want: "<nil>"},

		{noenv: "example.com", req: "http://example.com/", env: "proxy", want: "<nil>"},
		{noenv: ".example.com", req: "http://example.com/", env: "proxy", want: "<nil>"},
		{noenv: "ample.com", req: "http://example.com/", env: "proxy", want: "http://proxy"},
		{noenv: "example.com", req: "http://foo.example.com/", env: "proxy", want: "<nil>"},
		{noenv: ".foo.com", req: "http://example.com/", env: "proxy", want: "http://proxy"},
	}

	for _, tt := range proxyFromEnvTests {
		os.Setenv("HTTP_PROXY", tt.env)
		os.Setenv("HTTPS_PROXY", tt.httpsenv)
		os.Setenv("NO_PROXY", tt.noenv)
		os.Setenv("REQUEST_METHOD", tt.reqmeth)
		ResetCachedEnvironment()
		reqURL := tt.req
		if reqURL == "" {
			reqURL = "http://example.com"
		}
		req, _ := NewRequest(GET, reqURL, nil)
		testUrl, err := ProxyFromEnvironment(req)
		if g, e := fmt.Sprintf("%v", err), fmt.Sprintf("%v", tt.wanterr); g != e {
			t.Errorf("%v: got error = %q, want %q", tt, g, e)
			continue
		}
		if got := fmt.Sprintf("%s", testUrl); got != tt.want {
			t.Errorf("%v: got URL = %q, want %q", tt, testUrl, tt.want)
		}
	}
}

func TestIdleConnChannelLeak(t *testing.T) {
	// Not parallel: uses global test hooks.
	var mu sync.Mutex
	var n int

	ts := th.NewServer(HandlerFunc(func(w ResponseWriter, r *Request) {
		mu.Lock()
		n++
		mu.Unlock()
	}))
	defer ts.Close()

	const nReqs = 5
	didRead := make(chan bool, nReqs)

	// @comment : new way of waiting for server events
	eventHandler := ListenTestEvent(ReadLoopBeforeNextReadEvent, func() {
		didRead <- true
	})
	defer eventHandler.Kill()

	c := ts.Client()
	tr := c.Transport.(*Transport)
	tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		return net.Dial(network, ts.Listener.Addr().String())
	}
	// First, without keep-alives.
	for _, disableKeep := range []bool{true, false} {
		tr.DisableKeepAlives = disableKeep
		for i := 0; i < nReqs; i++ {
			_, err := c.Get(fmt.Sprintf("http://foo-host-%d.tld/", i))
			if err != nil {
				t.Fatal(err)
			}
			// Note: no res.Body.Close is needed here, since the
			// response Content-Length is zero. Perhaps the test
			// should be more explicit and use a HEAD, but tests
			// elsewhere guarantee that zero byte responses generate
			// a "Content-Length: 0" instead of chunking.
		}

		// At this point, each of the 5 Transport.readLoop goroutines
		// are scheduling noting that there are no response bodies (see
		// earlier comment), and are then calling putIdleConn, which
		// decrements this count. Usually that happens quickly, which is
		// why this test has seemed to work for ages. But it's still
		// racey: we have wait for them to finish first. See Issue 10427
		for i := 0; i < nReqs; i++ {
			<-didRead
		}

		if got := tr.IdleConnChMapSizeForTesting(); got != 0 {
			t.Fatalf("ForDisableKeepAlives = %v, map size = %d; want 0", disableKeep, got)
		}
	}
}

// Verify the status quo: that the Client.Post function coerces its
// body into a ReadCloser if it's a Closer, and that the Transport
// then closes it.
func TestTransportClosesRequestBody(t *testing.T) {
	defer afterTest(t)
	ts := th.NewServer(HandlerFunc(func(w ResponseWriter, r *Request) {
		io.Copy(ioutil.Discard, r.Body)
	}))
	defer ts.Close()

	c := ts.Client()

	closes := 0

	res, err := c.Post(ts.URL, "text/plain", countCloseReader{&closes, strings.NewReader("hello")})
	if err != nil {
		t.Fatal(err)
	}
	res.CloseBody()
	if closes != 1 {
		t.Errorf("closes = %d; want 1", closes)
	}
}

func TestTransportTLSHandshakeTimeout(t *testing.T) {
	defer afterTest(t)
	if testing.Short() {
		t.Skip("skipping in short mode")
	}
	ln := newLocalListener(t)
	defer ln.Close()
	testdonec := make(chan struct{})
	defer close(testdonec)

	go func() {
		c, err := ln.Accept()
		if err != nil {
			t.Error(err)
			return
		}
		<-testdonec
		c.Close()
	}()

	getdonec := make(chan struct{})
	go func() {
		defer close(getdonec)
		tr := &Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return net.Dial("tcp", ln.Addr().String())
			},
			TLSHandshakeTimeout: 250 * time.Millisecond,
		}
		cl := &cli.Client{Transport: tr}
		_, err := cl.Get("https://dummy.tld/")
		if err == nil {
			t.Error("expected error")
			return
		}
		ue, ok := err.(*url.Error)
		if !ok {
			t.Errorf("expected url.Error; got %#v", err)
			return
		}
		// TODO [COMMENT] : @badu - net.Error is an interface : Timeout() bool and Temporary() bool
		ne, ok := ue.Err.(net.Error)
		if !ok {
			t.Errorf("expected net.Error; got %#v", err)
			return
		}
		if !ne.Timeout() {
			t.Errorf("expected timeout error; got %v", err)
		}
		if !strings.Contains(err.Error(), "handshake timeout") {
			t.Errorf("expected 'handshake timeout' in error; got %v", err)
		}
	}()
	select {
	case <-getdonec:
	case <-time.After(5 * time.Second):
		t.Error("test timeout; TLS handshake hung?")
	}
}

// Trying to repro golang.org/issue/3514
func TestTLSServerClosesConnection(t *testing.T) {
	defer afterTest(t)
	SkipFlaky(t, 7634)

	closedc := make(chan bool, 1)
	ts := th.NewTLSServer(HandlerFunc(func(w ResponseWriter, r *Request) {
		if strings.Contains(r.URL.Path, "/keep-alive-then-die") {
			conn, _, _ := w.(Hijacker).Hijack()
			conn.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 3\r\n\r\nfoo"))
			conn.Close()
			closedc <- true
			return
		}
		fmt.Fprintf(w, "hello")
	}))
	defer ts.Close()

	c := ts.Client()
	tr := c.Transport.(*Transport)

	var nSuccess = 0
	var errs []error
	const trials = 20
	for i := 0; i < trials; i++ {
		tr.CloseIdleConnections()
		res, err := c.Get(ts.URL + "/keep-alive-then-die")
		if err != nil {
			t.Fatal(err)
		}
		<-closedc
		slurp, err := ioutil.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		if string(slurp) != "foo" {
			t.Errorf("Got %q, want foo", slurp)
		}

		// Now try again and see if we successfully
		// pick a new connection.
		res, err = c.Get(ts.URL + "/")
		if err != nil {
			errs = append(errs, err)
			continue
		}
		slurp, err = ioutil.ReadAll(res.Body)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		nSuccess++
	}
	if nSuccess > 0 {
		t.Logf("successes = %d of %d", nSuccess, trials)
	} else {
		t.Errorf("All runs failed:")
	}
	for _, err := range errs {
		t.Logf("  err: %v", err)
	}
}

// Verifies that the Transport doesn't reuse a connection in the case
// where the server replies before the request has been fully
// written. We still honor that reply (see TestIssue3595), but don't
// send future requests on the connection because it's then in a
// questionable state.
// golang.org/issue/7569
func TestTransportNoReuseAfterEarlyResponse(t *testing.T) {
	setParallel(t)
	defer afterTest(t)
	var sconn struct {
		sync.Mutex
		c net.Conn
	}
	var getOkay bool
	closeConn := func() {
		sconn.Lock()
		defer sconn.Unlock()
		if sconn.c != nil {
			sconn.c.Close()
			sconn.c = nil
			if !getOkay {
				t.Logf("Closed server connection")
			}
		}
	}
	defer closeConn()

	ts := th.NewServer(HandlerFunc(func(w ResponseWriter, r *Request) {
		if r.Method == GET {
			io.WriteString(w, "bar")
			return
		}
		conn, _, _ := w.(Hijacker).Hijack()
		sconn.Lock()
		sconn.c = conn
		sconn.Unlock()
		conn.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 3\r\n\r\nfoo")) // keep-alive
		go io.Copy(ioutil.Discard, conn)
	}))
	defer ts.Close()
	c := ts.Client()

	const bodySize = 256 << 10
	finalBit := make(byteFromChanReader, 1)
	req, _ := NewRequest(POST, ts.URL, io.MultiReader(io.LimitReader(neverEnding('x'), bodySize-1), finalBit))
	req.ContentLength = bodySize
	res, err := c.Do(req)
	if err := wantBody(res, err, "foo"); err != nil {
		t.Errorf("POST response: %v", err)
	}
	donec := make(chan bool)
	go func() {
		defer close(donec)
		res, err = c.Get(ts.URL)
		if err := wantBody(res, err, "bar"); err != nil {
			t.Errorf("GET response: %v", err)
			return
		}
		getOkay = true // suppress test noise
	}()
	time.AfterFunc(5*time.Second, closeConn)
	select {
	case <-donec:
		finalBit <- 'x' // unblock the writeloop of the first Post
		close(finalBit)
	case <-time.After(7 * time.Second):
		t.Fatal("timeout waiting for GET request to finish")
	}
}

// Tests that we don't leak Transport persistConn.readLoop goroutines
// when a server hangs up immediately after saying it would keep-alive.
func TestTransportIssue10457(t *testing.T) {
	defer afterTest(t) // used to fail in goroutine leak check
	ts := th.NewServer(HandlerFunc(func(w ResponseWriter, r *Request) {
		// Send a response with no body, keep-alive
		// (implicit), and then lie and immediately close the
		// connection. This forces the Transport's readLoop to
		// immediately Peek an io.EOF and get to the point
		// that used to hang.
		conn, _, _ := w.(Hijacker).Hijack()
		conn.Write([]byte("HTTP/1.1 200 OK\r\nFoo: Bar\r\nContent-Length: 0\r\n\r\n")) // keep-alive
		conn.Close()
	}))
	defer ts.Close()
	c := ts.Client()

	res, err := c.Get(ts.URL)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer res.CloseBody()

	// Just a sanity check that we at least get the response. The real
	// test here is that the "defer afterTest" above doesn't find any
	// leaked goroutines.
	if got, want := res.Header.Get("Foo"), "Bar"; got != want {
		t.Errorf("Foo header = %q; want %q", got, want)
	}
}

// Issues 4677, 18241, and 17844. If we try to reuse a connection that the
// server is in the process of closing, we may end up successfully writing out
// our request (or a portion of our request) only to find a connection error
// when we try to read from (or finish writing to) the socket.
//
// NOTE: we resend a request only if:
//   - we reused a keep-alive connection
//   - we haven't yet received any header data
//   - either we wrote no bytes to the server, or the request is idempotent
// This automatically prevents an infinite resend loop because we'll run out of
// the cached keep-alive connections eventually.
func TestRetryRequestsOnError(t *testing.T) {
	newRequest := func(method, urlStr string, body io.Reader) *Request {
		req, err := NewRequest(method, urlStr, body)
		if err != nil {
			t.Fatal(err)
		}
		return req
	}
	testCases := []struct {
		name       string
		failureN   int
		failureErr error
		// Note that we can't just re-use the Request object across calls to c.Do
		// because we need to rewind Body between calls.  (GetBody is only used to
		// rewind Body on failure and redirects, not just because it's done.)
		req       func() *Request
		reqString string
	}{
		{
			name: "IdempotentNoBodySomeWritten",
			// Believe that we've written some bytes to the server, so we know we're
			// not just in the "retry when no bytes sent" case".
			failureN: 1,
			// Use the specific error that shouldRetryRequest looks for with idempotent requests.
			failureErr: ErrServerClosedIdle,
			req: func() *Request {
				return newRequest(GET, "http://fake.golang", nil)
			},
			reqString: `GET / HTTP/1.1\r\nHost: fake.golang\r\nUser-Agent: Go-http-client/1.1\r\nAccept-Encoding: gzip\r\n\r\n`,
		},
		{
			name: "IdempotentGetBodySomeWritten",
			// Believe that we've written some bytes to the server, so we know we're
			// not just in the "retry when no bytes sent" case".
			failureN: 1,
			// Use the specific error that shouldRetryRequest looks for with idempotent requests.
			failureErr: ErrServerClosedIdle,
			req: func() *Request {
				return newRequest(GET, "http://fake.golang", strings.NewReader("foo\n"))
			},
			reqString: `GET / HTTP/1.1\r\nHost: fake.golang\r\nUser-Agent: Go-http-client/1.1\r\nContent-Length: 4\r\nAccept-Encoding: gzip\r\n\r\nfoo\n`,
		},
		{
			name: "NothingWrittenNoBody",
			// It's key that we return 0 here -- that's what enables Transport to know
			// that nothing was written, even though this is a non-idempotent request.
			failureN:   0,
			failureErr: errors.New("second write fails"),
			req: func() *Request {
				return newRequest(DELETE, "http://fake.golang", nil)
			},
			reqString: `DELETE / HTTP/1.1\r\nHost: fake.golang\r\nUser-Agent: Go-http-client/1.1\r\nAccept-Encoding: gzip\r\n\r\n`,
		},
		{
			name: "NothingWrittenGetBody",
			// It's key that we return 0 here -- that's what enables Transport to know
			// that nothing was written, even though this is a non-idempotent request.
			failureN:   0,
			failureErr: errors.New("second write fails"),
			// Note that NewRequest will set up GetBody for strings.Reader, which is
			// required for the retry to occur
			req: func() *Request {
				return newRequest(POST, "http://fake.golang", strings.NewReader("foo\n"))
			},
			reqString: `POST / HTTP/1.1\r\nHost: fake.golang\r\nUser-Agent: Go-http-client/1.1\r\nContent-Length: 4\r\nAccept-Encoding: gzip\r\n\r\nfoo\n`,
		},
	}
	var logf func(format string, args ...interface{})
	// @comment : new way of waiting for server events
	eventHandler := ListenTestEvent(RoundTripRetriedEvent, func() {
		logf("Retried.")
	})
	defer eventHandler.Kill()

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			defer afterTest(t)

			var (
				mu     sync.Mutex
				logbuf bytes.Buffer
			)

			logf = func(format string, args ...interface{}) {
				mu.Lock()
				defer mu.Unlock()
				fmt.Fprintf(&logbuf, format, args...)
				logbuf.WriteByte('\n')
			}

			ts := th.NewServer(HandlerFunc(func(w ResponseWriter, r *Request) {
				logf("Handler")
				w.Header().Set("X-Status", "ok")
			}))
			defer ts.Close()

			var writeNumAtomic int32
			c := ts.Client()
			c.Transport.(*Transport).DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
				logf("Dial")
				c, err := net.Dial(network, ts.Listener.Addr().String())
				if err != nil {
					logf("Dial error: %v", err)
					return nil, err
				}
				return &writerFuncConn{
					Conn: c,
					write: func(p []byte) (n int, err error) {
						if atomic.AddInt32(&writeNumAtomic, 1) == 2 {
							logf("intentional write failure")
							return tc.failureN, tc.failureErr
						}
						logf("Write(%q)", p)
						return c.Write(p)
					},
				}, nil
			}

			for i := 0; i < 3; i++ {
				res, err := c.Do(tc.req())
				if err != nil {
					t.Fatalf("i=%d: Do = %v", i, err)
				}
				res.CloseBody()
			}

			mu.Lock()
			got := logbuf.String()
			mu.Unlock()
			// @comment : what's interesting here is the fact that we're running concurently, but we're expecting a sequence
			want := fmt.Sprintf(`Dial
Write("%s")
Handler
intentional write failure
Retried.
Dial
Write("%s")
Handler
Write("%s")
Handler
`, tc.reqString, tc.reqString, tc.reqString)
			if got != want {
				t.Logf("Log of events differs (happens in concurency / doesn't happens in race). Got:\n%s\nWant:\n%s", got, want)
			}
		})
	}
}

// Issue 6981
func TestTransportClosesBodyOnError(t *testing.T) {
	setParallel(t)
	defer afterTest(t)
	readBody := make(chan error, 1)
	ts := th.NewServer(HandlerFunc(func(w ResponseWriter, r *Request) {
		_, err := ioutil.ReadAll(r.Body)
		readBody <- err
	}))
	defer ts.Close()
	c := ts.Client()
	fakeErr := errors.New("fake error")
	didClose := make(chan bool, 1)
	req, _ := NewRequest(POST, ts.URL, struct {
		io.Reader
		io.Closer
	}{
		io.MultiReader(io.LimitReader(neverEnding('x'), 1<<20), errorReader{fakeErr}),
		closerFunc(func() error {
			select {
			case didClose <- true:
			default:
			}
			return nil
		}),
	})
	res, err := c.Do(req)
	if res != nil {
		defer res.CloseBody()
	}
	if err == nil || !strings.Contains(err.Error(), fakeErr.Error()) {
		t.Fatalf("Do error = %v; want something containing %q", err, fakeErr.Error())
	}
	select {
	case err := <-readBody:
		if err == nil {
			t.Errorf("Unexpected success reading request body from handler; want 'unexpected EOF reading trailer'")
		}
	case <-time.After(5 * time.Second):
		t.Error("timeout waiting for server handler to complete")
	}
	select {
	case <-didClose:
	default:
		t.Errorf("didn't see Body.Close")
	}
}

func TestTransportDialTLS(t *testing.T) {
	setParallel(t)
	defer afterTest(t)
	var mu sync.Mutex // guards following
	var gotReq, didDial bool

	ts := th.NewTLSServer(HandlerFunc(func(w ResponseWriter, r *Request) {
		mu.Lock()
		gotReq = true
		mu.Unlock()
	}))
	defer ts.Close()
	c := ts.Client()
	c.Transport.(*Transport).DialTLS = func(netw, addr string) (net.Conn, error) {
		mu.Lock()
		didDial = true
		mu.Unlock()
		c, err := tls.Dial(netw, addr, c.Transport.(*Transport).TLSClientConfig)
		if err != nil {
			return nil, err
		}
		return c, c.Handshake()
	}

	res, err := c.Get(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	res.CloseBody()
	mu.Lock()
	if !gotReq {
		t.Error("didn't get request")
	}
	if !didDial {
		t.Error("didn't use dial hook")
	}
}

// Test for issue 8755
// Ensure that if a proxy returns an error, it is exposed by RoundTrip
func TestRoundTripReturnsProxyError(t *testing.T) {
	badProxy := func(*Request) (*url.URL, error) {
		return nil, errors.New("errorMessage")
	}

	tr := &Transport{Proxy: badProxy}

	req, _ := NewRequest(GET, "http://example.com", nil)

	_, err := tr.RoundTrip(req)

	if err == nil {
		t.Error("Expected proxy error to be returned by RoundTrip")
	}
}

// tests that putting an idle conn after a call to CloseIdleConns does return it
func TestTransportCloseIdleConnsThenReturn(t *testing.T) {
	tr := &Transport{}
	wantIdle := func(when string, n int) bool {
		got := tr.IdleConnCountForTesting("|http|example.com") // key used by PutIdleTestConn
		if got == n {
			return true
		}
		t.Errorf("%s: idle conns = %d; want %d", when, got, n)
		return false
	}
	wantIdle("start", 0)
	if !tr.PutIdleTestConn() {
		t.Fatal("put failed")
	}
	if !tr.PutIdleTestConn() {
		t.Fatal("second put failed")
	}
	wantIdle("after put", 2)
	tr.CloseIdleConnections()
	if !tr.IsIdleForTesting() {
		t.Error("should be idle after CloseIdleConnections")
	}
	wantIdle("after close idle", 0)
	if tr.PutIdleTestConn() {
		t.Fatal("put didn't fail")
	}
	wantIdle("after second put", 0)

	tr.RequestIdleConnChForTesting() // should toggle the transport out of idle mode
	if tr.IsIdleForTesting() {
		t.Error("shouldn't be idle after RequestIdleConnChForTesting")
	}
	if !tr.PutIdleTestConn() {
		t.Fatal("after re-activation")
	}
	wantIdle("after final put", 1)
}

// This tests that an client requesting a content range won't also
// implicitly ask for gzip support. If they want that, they need to do it
// on their own.
// golang.org/issue/8923
func TestTransportRangeAndGzip(t *testing.T) {
	defer afterTest(t)
	reqc := make(chan *Request, 1)
	ts := th.NewServer(HandlerFunc(func(w ResponseWriter, r *Request) {
		reqc <- r
	}))
	defer ts.Close()
	c := ts.Client()

	req, _ := NewRequest(GET, ts.URL, nil)
	req.Header.Set("Range", "bytes=7-11")
	res, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}

	select {
	case r := <-reqc:
		if strings.Contains(r.Header.Get(AcceptEncoding), "gzip") {
			t.Error("Transport advertised gzip support in the Accept header")
		}
		if r.Header.Get("Range") == "" {
			t.Error("no Range in request")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timeout")
	}
	res.CloseBody()
}

// Test for issue 10474
func TestTransportResponseCancelRace(t *testing.T) {
	defer afterTest(t)

	ts := th.NewServer(HandlerFunc(func(w ResponseWriter, r *Request) {
		// important that this response has a body.
		var b [1024]byte
		w.Write(b[:])
	}))
	defer ts.Close()
	tr := ts.Client().Transport.(*Transport)

	req, err := NewRequest(GET, ts.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	// If we do an early close, Transport just throws the connection away and
	// doesn't reuse it. In order to trigger the bug, it has to reuse the connection
	// so read the body
	if _, err := io.Copy(ioutil.Discard, res.Body); err != nil {
		t.Fatal(err)
	}

	req2, err := NewRequest(GET, ts.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	//tr.CancelRequest(req)
	res, err = tr.RoundTrip(req2)
	if err != nil {
		t.Fatal(err)
	}
	res.CloseBody()
}

// Test for issue 19248: Content-Encoding's value is case insensitive.
func TestTransportContentEncodingCaseInsensitive(t *testing.T) {
	setParallel(t)
	defer afterTest(t)
	for _, ce := range []string{"gzip", "GZIP"} {
		ce := ce
		t.Run(ce, func(t *testing.T) {
			const encodedString = "Hello Gopher"
			ts := th.NewServer(HandlerFunc(func(w ResponseWriter, r *Request) {
				w.Header().Set(ContentEncoding, ce)
				gz := gzip.NewWriter(w)
				gz.Write([]byte(encodedString))
				gz.Close()
			}))
			defer ts.Close()

			res, err := ts.Client().Get(ts.URL)
			if err != nil {
				t.Fatal(err)
			}

			body, err := ioutil.ReadAll(res.Body)
			res.CloseBody()
			if err != nil {
				t.Fatal(err)
			}

			if string(body) != encodedString {
				t.Fatalf("Expected body %q, got: %q\n", encodedString, string(body))
			}
		})
	}
}

// Issue 6574
func TestTransportFlushesBodyChunks(t *testing.T) {
	defer afterTest(t)
	resBody := make(chan io.Reader, 1)
	connr, connw := io.Pipe() // connection pipe pair
	lw := &logWritesConn{
		rch: resBody,
		w:   connw,
	}
	tr := &Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return lw, nil
		},
	}
	bodyr, bodyw := io.Pipe() // body pipe pair
	go func() {
		defer bodyw.Close()
		for i := 0; i < 3; i++ {
			fmt.Fprintf(bodyw, "num%d\n", i)
		}
	}()
	resc := make(chan *Response)
	go func() {
		req, _ := NewRequest(POST, "http://localhost:8080", bodyr)
		req.Header.Set(UserAgent, "x") // known value for test
		res, err := tr.RoundTrip(req)
		if err != nil {
			t.Errorf("RoundTrip: %v", err)
			close(resc)
			return
		}
		resc <- res

	}()
	// Fully consume the request before checking the Write log vs. want.
	req, err := ReadRequest(bufio.NewReader(connr))
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(ioutil.Discard, req.Body)

	// Unblock the transport's roundTrip goroutine.
	resBody <- strings.NewReader("HTTP/1.1 204 No Content\r\nConnection: close\r\n\r\n")
	res, ok := <-resc
	if !ok {
		return
	}
	defer res.CloseBody()

	want := []string{
		"POST / HTTP/1.1\r\nHost: localhost:8080\r\nUser-Agent: x\r\nTransfer-Encoding: chunked\r\nAccept-Encoding: gzip\r\n\r\n" +
			"5\r\nnum0\n\r\n",
		"5\r\nnum1\n\r\n",
		"5\r\nnum2\n\r\n",
		"0\r\n\r\n",
	}
	if !reflect.DeepEqual(lw.writes, want) {
		t.Errorf("Writes differed.\n Got: %q\nWant: %q\n", lw.writes, want)
	}
}

// Issue 11745.
func TestTransportPrefersResponseOverWriteError(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}
	defer afterTest(t)
	const contentLengthLimit = 1024 * 1024 // 1MB
	ts := th.NewServer(HandlerFunc(func(w ResponseWriter, r *Request) {
		if r.ContentLength >= contentLengthLimit {
			w.WriteHeader(StatusBadRequest)
			r.CloseBody()
			return
		}
		w.WriteHeader(StatusOK)
	}))
	defer ts.Close()
	c := ts.Client()

	fail := 0
	count := 100
	bigBody := strings.Repeat("a", contentLengthLimit*2)
	for i := 0; i < count; i++ {
		req, err := NewRequest(PUT, ts.URL, strings.NewReader(bigBody))
		if err != nil {
			t.Fatal(err)
		}
		resp, err := c.Do(req)
		if err != nil {
			fail++
			t.Logf("%d = %#v", i, err)
			if ue, ok := err.(*url.Error); ok {
				t.Logf("urlErr = %#v", ue.Err)
				if ne, ok := ue.Err.(*net.OpError); ok {
					t.Logf("netOpError = %#v", ne.Err)
				}
			}
		} else {
			resp.CloseBody()
			if resp.StatusCode != 400 {
				t.Errorf("Expected status code 400, got %v", resp.Status)
			}
		}
	}
	if fail > 0 {
		t.Errorf("Failed %v out of %v\n", fail, count)
	}
}

// Issue 13633: there was a race where we returned bodyless responses
// to callers before recycling the persistent connection, which meant
// a client doing two subsequent requests could end up on different
// connections. It's somewhat harmless but enough tests assume it's
// not true in order to test other things that it's worth fixing.
// Plus it's nice to be consistent and not have timing-dependent
// behavior.
func TestTransportReuseConnEmptyResponseBody(t *testing.T) {
	defer afterTest(t)
	cst := newClientServerTest(t, HandlerFunc(func(w ResponseWriter, r *Request) {
		w.Header().Set("X-Addr", r.RemoteAddr)
		// Empty response body.
	}))
	defer cst.close()
	n := 100
	if testing.Short() {
		n = 10
	}
	var firstAddr string
	for i := 0; i < n; i++ {
		res, err := cst.c.Get(cst.ts.URL)
		if err != nil {
			log.Fatal(err)
		}
		addr := res.Header.Get("X-Addr")
		if i == 0 {
			firstAddr = addr
		} else if addr != firstAddr {
			t.Fatalf("On request %d, addr %q != original addr %q", i+1, addr, firstAddr)
		}
		res.CloseBody()
	}
}

func TestTransportReuseConnectionGzipChunked(t *testing.T) {
	testTransportReuseConnectionGzip(t, true)
}

func TestTransportReuseConnectionGzipContentLength(t *testing.T) {
	testTransportReuseConnectionGzip(t, false)
}

// Make sure we re-use underlying TCP connection for gzipped responses too.
func testTransportReuseConnectionGzip(t *testing.T, chunked bool) {
	setParallel(t)
	defer afterTest(t)
	addr := make(chan string, 2)
	ts := th.NewServer(HandlerFunc(func(w ResponseWriter, r *Request) {
		addr <- r.RemoteAddr
		w.Header().Set(ContentEncoding, "gzip")
		if chunked {
			w.(Flusher).Flush()
		}
		w.Write(rgz) // arbitrary gzip response
	}))
	defer ts.Close()
	c := ts.Client()

	for i := 0; i < 2; i++ {
		res, err := c.Get(ts.URL)
		if err != nil {
			t.Fatal(err)
		}
		buf := make([]byte, len(rgz))
		if n, err := io.ReadFull(res.Body, buf); err != nil {
			t.Errorf("%d. ReadFull = %v, %v", i, n, err)
		}
		// Note: no res.Body.Close call. It should work without it,
		// since the flate.Reader's internal buffering will hit EOF
		// and that should be sufficient.
	}
	a1, a2 := <-addr, <-addr
	if a1 != a2 {
		t.Fatalf("didn't reuse connection")
	}
}

func TestTransportResponseHeaderLength(t *testing.T) {
	setParallel(t)
	defer afterTest(t)
	ts := th.NewServer(HandlerFunc(func(w ResponseWriter, r *Request) {
		if r.URL.Path == "/long" {
			w.Header().Set("Long", strings.Repeat("a", 1<<20))
		}
	}))
	defer ts.Close()
	c := ts.Client()
	c.Transport.(*Transport).MaxResponseHeaderBytes = 512 << 10

	if res, err := c.Get(ts.URL); err != nil {
		t.Fatal(err)
	} else {
		res.CloseBody()
	}

	res, err := c.Get(ts.URL + "/long")
	if err == nil {
		defer res.CloseBody()
		var n int64
		for k, vv := range res.Header {
			for _, v := range vv {
				n += int64(len(k)) + int64(len(v))
			}
		}
		t.Fatalf("Unexpected success. Got %v and %d bytes of response headers", res.Status, n)
	}
	if want := "server response headers exceeded 524288 bytes"; !strings.Contains(err.Error(), want) {
		t.Errorf("got error: %v; want %q", err, want)
	}
}

// Issue 14353: port can only contain digits.
func TestTransportRejectsAlphaPort(t *testing.T) {
	res, err := cli.Get("http://dummy.tld:123foo/bar")
	if err == nil {
		res.CloseBody()
		t.Fatal("unexpected success")
	}
	ue, ok := err.(*url.Error)
	if !ok {
		t.Fatalf("got %#v; want *url.Error", err)
	}
	got := ue.Err.Error()
	want := `invalid URL port "123foo"`
	if got != want {
		t.Errorf("got error %q; want %q", got, want)
	}
}

// Test the trace.TLSHandshake{Start,Done} hooks with a https http1
// connections. The http2 test is done in TestTransportEventTrace_h2
func TestTLSHandshakeTrace(t *testing.T) {
	defer afterTest(t)
	ts := th.NewTLSServer(HandlerFunc(func(w ResponseWriter, r *Request) {}))
	defer ts.Close()

	var mu sync.Mutex
	var start, done bool
	tracer := &trc.ClientTrace{
		TLSHandshakeStart: func() {
			mu.Lock()
			defer mu.Unlock()
			start = true
		},
		TLSHandshakeDone: func(s tls.ConnectionState, err error) {
			mu.Lock()
			defer mu.Unlock()
			done = true
			if err != nil {
				t.Fatal("Expected error to be nil but was:", err)
			}
		},
	}

	c := ts.Client()
	req, err := NewRequest(GET, ts.URL, nil)
	if err != nil {
		t.Fatal("Unable to construct test request:", err)
	}
	req = req.WithContext(trc.WithClientTrace(req.Context(), tracer))

	r, err := c.Do(req)
	if err != nil {
		t.Fatal("Unexpected error making request:", err)
	}
	r.CloseBody()
	mu.Lock()
	defer mu.Unlock()
	if !start {
		t.Fatal("Expected TLSHandshakeStart to be called, but wasn't")
	}
	if !done {
		t.Fatal("Expected TLSHandshakeDone to be called, but wasnt't")
	}
}

func TestTransportIdleConnTimeout(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}
	defer afterTest(t)

	const timeout = 1 * time.Second

	cst := newClientServerTest(t, HandlerFunc(func(w ResponseWriter, r *Request) {
		// No body for convenience.
	}))
	defer cst.close()
	tr := cst.tr
	tr.IdleConnTimeout = timeout
	defer tr.CloseIdleConnections()
	c := &cli.Client{Transport: tr}

	idleConns := func() []string {
		return tr.IdleConnStrsForTesting()
	}

	var conn string
	doReq := func(n int) {
		req, _ := NewRequest(GET, cst.ts.URL, nil)
		req = req.WithContext(trc.WithClientTrace(context.Background(), &trc.ClientTrace{
			PutIdleConn: func(err error) {
				if err != nil {
					t.Errorf("failed to keep idle conn: %v", err)
				}
			},
		}))
		res, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.CloseBody()
		conns := idleConns()
		if len(conns) != 1 {
			t.Fatalf("req %v: unexpected number of idle conns: %q", n, conns)
		}
		if conn == "" {
			conn = conns[0]
		}
		if conn != conns[0] {
			t.Fatalf("req %v: cached connection changed; expected the same one throughout the test", n)
		}
	}
	for i := 0; i < 3; i++ {
		doReq(i)
		time.Sleep(timeout / 2)
	}
	time.Sleep(timeout * 3 / 2)
	if got := idleConns(); len(got) != 0 {
		t.Errorf("idle conns = %q; want none", got)
	}
}

// Issue 16465: Transport.RoundTrip should return the raw net.Conn.Read error from Peek
// back to the caller.
func TestTransportReturnsPeekError(t *testing.T) {
	errValue := errors.New("specific error value")

	wrote := make(chan struct{})
	var wroteOnce sync.Once

	tr := &Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			c := funcConn{
				read: func([]byte) (int, error) {
					<-wrote
					return 0, errValue
				},
				write: func(p []byte) (int, error) {
					wroteOnce.Do(func() { close(wrote) })
					return len(p), nil
				},
			}
			return c, nil
		},
	}
	_, err := tr.RoundTrip(th.NewTRequest(GET, "http://fake.tld/", nil))
	if err != errValue {
		t.Errorf("error = %#v; want %v", err, errValue)
	}
}

// Issue 13290: send User-Agent in proxy CONNECT
func TestTransportProxyConnectHeader(t *testing.T) {
	defer afterTest(t)
	reqc := make(chan *Request, 1)
	ts := th.NewServer(HandlerFunc(func(w ResponseWriter, r *Request) {
		if r.Method != CONNECT {
			t.Errorf("method = %q; want CONNECT", r.Method)
		}
		reqc <- r
		c, _, err := w.(Hijacker).Hijack()
		if err != nil {
			t.Errorf("Hijack: %v", err)
			return
		}
		c.Close()
	}))
	defer ts.Close()

	c := ts.Client()
	c.Transport.(*Transport).Proxy = func(r *Request) (*url.URL, error) {
		return url.Parse(ts.URL)
	}
	c.Transport.(*Transport).ProxyConnectHeader = Header{
		UserAgent: {"foo"},
		"Other":   {"bar"},
	}

	res, err := c.Get("https://dummy.tld/") // https to force a CONNECT
	if err == nil {
		res.CloseBody()
		t.Errorf("unexpected success")
	}
	select {
	case <-time.After(3 * time.Second):
		t.Fatal("timeout")
	case r := <-reqc:
		if got, want := r.Header.Get(UserAgent), "foo"; got != want {
			t.Errorf("CONNECT request User-Agent = %q; want %q", got, want)
		}
		if got, want := r.Header.Get("Other"), "bar"; got != want {
			t.Errorf("CONNECT request Other = %q; want %q", got, want)
		}
	}
}
