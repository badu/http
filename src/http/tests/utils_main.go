/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package tests

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"net/textproto"
	"os"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	. "http"
	"http/cli"
)

func (c *rwTestConn) Close() error {
	if c.closeFunc != nil {
		return c.closeFunc()
	}
	select {
	case c.closec <- true:
	default:
	}
	return nil
}

func (l *oneConnListener) Accept() (c net.Conn, err error) {
	c = l.conn
	if c == nil {
		err = io.EOF
		return
	}
	err = nil
	l.conn = nil
	return
}

func (l *oneConnListener) Close() error {
	return nil
}

func (l *oneConnListener) Addr() net.Addr {
	return dummyAddr("test-address")
}

func (tt runWrapper) reqFunc() reqFunc {
	if tt.ReqFunc == nil {
		return (*cli.Client).Get
	}
	return tt.ReqFunc
}

func (tt runWrapper) run(t *testing.T) {
	setParallel(t)
	cst1 := newClientServerTest(t, HandlerFunc(tt.Handler), tt.Opts...)
	defer cst1.close()

	res1, err := tt.reqFunc()(cst1.c, cst1.ts.URL)
	if err != nil {
		t.Errorf("HTTP/1 request: %v", err)
		return
	}

	if fn := tt.EarlyCheckResponse; fn != nil {
		fn(res1)
	}

	tt.normalizeRes(t, res1)

	if fn := tt.CheckResponse; fn != nil {
		fn(res1)
	}
}

func (tt runWrapper) normalizeRes(t *testing.T, res *Response) {
	if res.Proto == HTTP1_1 {
		res.Proto, res.ProtoMajor, res.ProtoMinor = "", 0, 0
	} else {
		t.Errorf("got %q response; want %q", res.Proto, HTTP1_1)
	}
	slurp, err := ioutil.ReadAll(res.Body)

	res.Body.Close()
	res.Body = slurpResult{
		ReadCloser: ioutil.NopCloser(bytes.NewReader(slurp)),
		body:       slurp,
		err:        err,
	}
	for i, v := range res.Header["Date"] {
		res.Header["Date"][i] = strings.Repeat("x", len(v))
	}
	if res.Request == nil {
		t.Errorf("for %s, no request", HTTP1_1)
	}
}

func interestingGoroutines() (gs []string) {
	buf := make([]byte, 2<<20)
	buf = buf[:runtime.Stack(buf, true)]
	for _, g := range strings.Split(string(buf), "\n\n") {
		sl := strings.SplitN(g, "\n", 2)
		if len(sl) != 2 {
			continue
		}
		stack := strings.TrimSpace(sl[1])
		//fmt.Fprintf(os.Stderr,"DAS STaCK : %q\n", stack)
		if stack == "" ||
			strings.Contains(stack, "testing.(*M).before.func1") ||
			strings.Contains(stack, "os/signal.signal_recv") ||
			strings.Contains(stack, "created by net.startServer") ||
			strings.Contains(stack, "created by testing.RunTests") ||
			strings.Contains(stack, "closeWriteAndWait") ||
			strings.Contains(stack, "testing.Main(") ||
			// These only show up with GOTRACEBACK=2; Issue 5005 (comment 28)
			strings.Contains(stack, "runtime.goexit") ||
			strings.Contains(stack, "created by runtime.gc") ||
			strings.Contains(stack, "net/ehttp/tests.interestingGoroutines") ||
			strings.Contains(stack, "runtime.MHeap_Scavenger") {
			continue
		}
		gs = append(gs, stack)
	}
	sort.Strings(gs)
	return
}

// Verify the other tests didn't leave any goroutines running.
func goroutineLeaked() bool {
	if testing.Short() || runningBenchmarks() {
		// Don't worry about goroutine leaks in -short mode or in
		// benchmark mode. Too distracting when there are false positives.
		return false
	}

	var stackCount map[string]int
	for i := 0; i < 5; i++ {
		n := 0
		stackCount = make(map[string]int)
		gs := interestingGoroutines()
		for _, g := range gs {
			stackCount[g]++
			n++
		}
		if n == 0 {
			return false
		}
		// Wait for goroutines to schedule and die off:
		time.Sleep(100 * time.Millisecond)
	}
	fmt.Fprintf(os.Stderr, "Too many goroutines running after net/http test(s).\n")
	for stack, count := range stackCount {
		fmt.Fprintf(os.Stderr, "%d instances of:\n%s\n", count, stack)
	}
	return true
}

// setParallel marks t as a parallel test if we're in short mode
// (all.bash), but as a serial test otherwise. Using t.Parallel isn't
// compatible with the afterTest func in non-short mode.
func setParallel(t *testing.T) {
	if testing.Short() {
		t.Parallel()
	}
}

func runningBenchmarks() bool {
	for i, arg := range os.Args {
		if strings.HasPrefix(arg, "-test.bench=") && !strings.HasSuffix(arg, "=") {
			return true
		}
		if arg == "-test.bench" && i < len(os.Args)-1 && os.Args[i+1] != "" {
			return true
		}
	}
	return false
}

func afterTest(t testing.TB) {
	DefaultTransport.(*Transport).CloseIdleConnections()
	if testing.Short() {
		return
	}
	var bad string
	badSubstring := map[string]string{
		").readLoop(":                                  "a Transport",
		").writeLoop(":                                 "a Transport",
		"created by net/http/httptest.(*Server).Start": "an httptest.Server",
		"timeoutHandler":                               "a TimeoutHandler",
		"net.(*netFD).connect(":                        "a timing out dial",
		").noteClientGone(":                            "a closenotifier sender",
	}
	var stacks string
	for i := 0; i < 4; i++ {
		bad = ""
		stacks = strings.Join(interestingGoroutines(), "\n\n")
		//fmt.Fprintf(os.Stderr,"Stacks : %q\n", stacks)
		for substr, what := range badSubstring {
			if strings.Contains(stacks, substr) {
				bad = what
			}
		}
		if bad == "" {
			return
		}
		// Bad stuff found, but goroutines might just still be
		// shutting down, so give it some time.
		time.Sleep(250 * time.Millisecond)
	}
	t.Errorf("Test appears to have leaked %s:\n%s", bad, stacks)
}

// waitCondition reports whether fn eventually returned true,
// checking immediately and then every checkEvery amount,
// until waitFor has elapsed, at which point it returns false.
func waitCondition(waitFor, checkEvery time.Duration, fn func() bool) bool {
	deadline := time.Now().Add(waitFor)
	for time.Now().Before(deadline) {
		if fn() {
			return true
		}
		time.Sleep(checkEvery)
	}
	return false
}

// waitErrCondition is like waitCondition but with errors instead of bools.
func waitErrCondition(waitFor, checkEvery time.Duration, fn func() error) error {
	deadline := time.Now().Add(waitFor)
	var err error
	for time.Now().Before(deadline) {
		if err = fn(); err == nil {
			return nil
		}
		time.Sleep(checkEvery)
	}
	return err
}

func NewTestTimeoutHandler(handler Handler, ch <-chan time.Time) Handler {
	return NewBodylessTimeoutHandler(handler, ch)
}

func ResetCachedEnvironment() {
	HttpProxyEnv.Reset()
	HttpsProxyEnv.Reset()
	NoProxyEnv.Reset()
}

// foreachHeaderElement splits v according to the "#rule" construction
// in RFC 2616 section 2.1 and calls fn for each non-empty element.
func foreachHeaderElement(v string, fn func(string)) {
	v = textproto.TrimString(v)
	if v == "" {
		return
	}
	if !strings.Contains(v, ",") {
		fn(v)
		return
	}
	for _, f := range strings.Split(v, ",") {
		if f = textproto.TrimString(f); f != "" {
			fn(f)
		}
	}
}

func NewLoggingConn(baseName string, c net.Conn) net.Conn {
	UniqNameMu.Lock()
	defer UniqNameMu.Unlock()
	UniqNameNext[baseName]++
	return &loggingConn{
		name: fmt.Sprintf("%s-%d", baseName, UniqNameNext[baseName]),
		Conn: c,
	}
}

func ResetProxyEnv() {
	for _, v := range []string{"HTTP_PROXY", "http_proxy", "NO_PROXY", "no_proxy"} {
		os.Unsetenv(v)
	}
	ResetCachedEnvironment()
}

var raceEnabled = false // set by race.go

func wantBody(res *Response, err error, want string) error {
	if err != nil {
		return err
	}
	slurp, err := ioutil.ReadAll(res.Body)
	if err != nil {
		return fmt.Errorf("error reading body: %v", err)
	}
	if string(slurp) != want {
		return fmt.Errorf("body = %q; want %q", slurp, want)
	}
	if err := res.Body.Close(); err != nil {
		return fmt.Errorf("body Close = %v", err)
	}
	return nil
}

func newLocalListener(t *testing.T) net.Listener {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		ln, err = net.Listen("tcp6", "[::1]:0")
	}
	if err != nil {
		t.Fatal(err)
	}
	return ln
}
