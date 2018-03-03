/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package tests

import (
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"strings"
	"sync/atomic"
	"testing"

	. "github.com/badu/http"
	"github.com/badu/http/cli"
	"github.com/badu/http/hdr"
	"github.com/badu/http/th"
	. "github.com/badu/http/tport"
	"github.com/badu/http/url"
)

func (b issue18239Body) Read([]byte) (int, error) {
	atomic.AddInt32(b.readCalls, 1)
	return 0, b.readErr
}

func (b issue18239Body) Close() error {
	atomic.AddInt32(b.closeCalls, 1)
	return nil
}

func (issue15577Tripper) RoundTrip(*Request) (*Response, error) {
	resp := &Response{
		StatusCode: 303,
		Header:     map[string][]string{hdr.Location: {"http://www.example.com/"}},
		Body:       ioutil.NopCloser(strings.NewReader("")),
	}
	return resp, nil
}

func (f eofReaderFunc) Read(p []byte) (n int, err error) {
	f()
	return 0, io.EOF
}

func (c *writeCountingConn) Write(p []byte) (int, error) {
	*c.count++
	return c.Conn.Write(p)
}

func (t *recordingTransport) RoundTrip(req *Request) (resp *Response, err error) {
	t.req = req
	return nil, errors.New("dummy impl")
}

func (j *RecordingJar) SetCookies(u *url.URL, cookies []*cli.Cookie) {
	j.logf("SetCookie(%q, %v)\n", u, cookies)
}

func (j *RecordingJar) Cookies(u *url.URL) []*cli.Cookie {
	j.logf("Cookies(%q)\n", u)
	return nil
}

func (j *RecordingJar) logf(format string, args ...interface{}) {
	j.mu.Lock()
	defer j.mu.Unlock()
	fmt.Fprintf(&j.log, format, args...)
}

func (w chanWriter) Write(p []byte) (int, error) {
	//noinspection GoAssignmentToReceiver
	w <- string(p)
	return len(p), nil
}

func (j *TestJar) SetCookies(u *url.URL, cookies []*cli.Cookie) {
	j.m.Lock()
	defer j.m.Unlock()
	if j.perURL == nil {
		j.perURL = make(map[string][]*cli.Cookie)
	}
	j.perURL[u.Host] = cookies
}

func (j *TestJar) Cookies(u *url.URL) []*cli.Cookie {
	j.m.Lock()
	defer j.m.Unlock()
	return j.perURL[u.Host]
}

func (t *clientServerTest) close() {
	t.tr.CloseIdleConnections()
	t.ts.Close()
}

func (t *clientServerTest) getURL(u string) string {
	res, err := t.c.Get(u)
	if err != nil {
		t.t.Fatal(err)
	}
	defer res.CloseBody()
	slurp, err := ioutil.ReadAll(res.Body)
	if err != nil {
		t.t.Fatal(err)
	}
	return string(slurp)
}

func (t *clientServerTest) scheme() string {
	return HTTP
}

func (sr slurpResult) String() string { return fmt.Sprintf("body %q; err %v", sr.body, sr.err) }

func (b *lockedBytesBuffer) Write(p []byte) (int, error) {
	b.Lock()
	defer b.Unlock()
	return b.Buffer.Write(p)
}

func (r testErrorReader) Read(p []byte) (n int, err error) {
	r.t.Error("unexpected Read call")
	return 0, io.EOF
}

func removeCommonLines(a, b string) (asuffix, bsuffix string, commonLines int) {
	for {
		nl := strings.IndexByte(a, '\n')
		if nl < 0 {
			return a, b, commonLines
		}
		line := a[:nl+1]
		if !strings.HasPrefix(b, line) {
			return a, b, commonLines
		}
		commonLines++
		a = a[len(line):]
		b = b[len(line):]
	}
}

func matchReturnedCookies(t *testing.T, expected, given []*cli.Cookie) {
	if len(given) != len(expected) {
		t.Logf("Received cookies: %v", given)
		t.Errorf("Expected %d cookies, got %d", len(expected), len(given))
	}
	for _, ec := range expected {
		foundC := false
		for _, c := range given {
			if ec.Name == c.Name && ec.Value == c.Value {
				foundC = true
				break
			}
		}
		if !foundC {
			t.Errorf("Missing cookie %v", ec)
		}
	}
}

func newClientServerTest(t *testing.T, h Handler, opts ...interface{}) *clientServerTest {
	cst := &clientServerTest{
		t:  t,
		h:  h,
		tr: &Transport{},
	}
	cst.c = &cli.Client{Transport: cst.tr}
	cst.ts = th.NewUnstartedServer(h)

	for _, opt := range opts {
		switch opt := opt.(type) {
		case func(*Transport):
			opt(cst.tr)
		case func(*th.TestServer):
			opt(cst.ts)
		default:
			t.Fatalf("unhandled option type %T", opt)
		}
	}

	cst.ts.Start()
	return cst
}

// pedanticReadAll works like ioutil.ReadAll but additionally
// verifies that r obeys the documented io.Reader contract.
func pedanticReadAll(r io.Reader) (b []byte, err error) {
	var bufa [64]byte
	buf := bufa[:]
	for {
		n, err := r.Read(buf)
		if n == 0 && err == nil {
			return nil, fmt.Errorf("Read: n=0 with err=nil")
		}
		b = append(b, buf[:n]...)
		if err == io.EOF {
			n, err := r.Read(buf)
			if n != 0 || err != io.EOF {
				return nil, fmt.Errorf("Read: n=%d err=%#v after EOF", n, err)
			}
			return b, nil
		}
		if err != nil {
			return b, err
		}
	}
}
