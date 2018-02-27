/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package tests

import (
	"bufio"
	"bytes"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	. "github.com/badu/http"
	"github.com/badu/http/cli"
	"github.com/badu/http/mux"
	"github.com/badu/http/th"
)

func BenchmarkHeaderWriteSubset(b *testing.B) {
	var buf bytes.Buffer
	var testHeader = Header{
		ContentLength: {"123"},
		ContentType:   {"text/plain"},
		Date:          {"some date at some time Z"},
		ServerHeader:  {DefaultUserAgent},
	}

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		buf.Reset()
		testHeader.WriteSubset(&buf, nil)
	}
}

func BenchmarkServeMux(b *testing.B) {
	type test struct {
		path string
		code int
		req  *Request
	}

	// Build example handlers and requests
	var tests []test
	endpoints := []string{"search", "dir", "file", "change", "count", "s"}
	for _, e := range endpoints {
		for i := 200; i < 230; i++ {
			p := fmt.Sprintf("/%s/%d/", e, i)
			tests = append(tests, test{
				path: p,
				code: i,
				req:  &Request{Method: GET, Host: "localhost", URL: &url.URL{Path: p}},
			})
		}
	}
	srvMx := mux.NewServeMux()
	for _, tt := range tests {
		srvMx.Handle(tt.path, serve(tt.code))
	}

	rw := th.NewRecorder()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, tt := range tests {
			*rw = th.ResponseRecorder{}
			h, pattern := srvMx.Handler(tt.req)
			h.ServeHTTP(rw, tt.req)
			if pattern != tt.path || rw.Code != tt.code {
				b.Fatalf("got %d, %q, want %d, %q", rw.Code, pattern, tt.code, tt.path)
			}
		}
	}
}

func BenchmarkClientServerParallel4(b *testing.B) {
	benchmarkClientServerParallel(b, 4, false)
}

func BenchmarkClientServerParallel64(b *testing.B) {
	benchmarkClientServerParallel(b, 64, false)
}

func BenchmarkClientServerParallelTLS4(b *testing.B) {
	benchmarkClientServerParallel(b, 4, true)
}

func BenchmarkClientServerParallelTLS64(b *testing.B) {
	benchmarkClientServerParallel(b, 64, true)
}

func benchmarkClientServerParallel(b *testing.B, parallelism int, useTLS bool) {
	b.ReportAllocs()
	ts := th.NewUnstartedServer(HandlerFunc(func(rw ResponseWriter, r *Request) {
		fmt.Fprintf(rw, "Hello world.\n")
	}))
	if useTLS {
		ts.StartTLS()
	} else {
		ts.Start()
	}
	defer ts.Close()
	b.ResetTimer()
	b.SetParallelism(parallelism)
	b.RunParallel(func(pb *testing.PB) {
		c := ts.Client()
		for pb.Next() {
			res, err := c.Get(ts.URL)
			if err != nil {
				b.Logf("Get: %v", err)
				continue
			}
			all, err := ioutil.ReadAll(res.Body)
			res.CloseBody()
			if err != nil {
				b.Logf("ReadAll: %v", err)
				continue
			}
			body := string(all)
			if body != "Hello world.\n" {
				panic("Got body: " + body)
			}
		}
	})
}

// A benchmark for profiling the server without the HTTP client code.
// The client code runs in a subprocess.
//
// For use like:
//   $ go test -c
//   $ ./http.test -test.run=XX -test.bench=BenchmarkServer -test.benchtime=15s -test.cpuprofile=http.prof
//   $ go tool pprof http.test http.prof
//   (pprof) web
func BenchmarkServer(b *testing.B) {
	b.ReportAllocs()
	// Child process mode;
	if benchUrl := os.Getenv("TEST_BENCH_SERVER_URL"); benchUrl != "" {
		n, err := strconv.Atoi(os.Getenv("TEST_BENCH_CLIENT_N"))
		if err != nil {
			panic(err)
		}
		for i := 0; i < n; i++ {
			res, err := cli.Get(benchUrl)
			if err != nil {
				log.Panicf("Get: %v", err)
			}
			all, err := ioutil.ReadAll(res.Body)
			res.CloseBody()
			if err != nil {
				log.Panicf("ReadAll: %v", err)
			}
			body := string(all)
			if body != "Hello world.\n" {
				log.Panicf("Got body: %q", body)
			}
		}
		os.Exit(0)
		return
	}

	var res = []byte("Hello world.\n")
	b.StopTimer()
	ts := th.NewServer(HandlerFunc(func(rw ResponseWriter, r *Request) {
		rw.Header().Set(ContentType, "text/html; charset=utf-8")
		rw.Write(res)
	}))
	defer ts.Close()
	b.StartTimer()

	cmd := exec.Command(os.Args[0], "-test.run=XXXX", "-test.bench=BenchmarkServer$")
	cmd.Env = append([]string{
		fmt.Sprintf("TEST_BENCH_CLIENT_N=%d", b.N),
		fmt.Sprintf("TEST_BENCH_SERVER_URL=%s", ts.URL),
	}, os.Environ()...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		b.Errorf("Test failure: %v, with output: %s", err, out)
	}
}

func BenchmarkServerFakeConnNoKeepAlive(b *testing.B) {
	b.ReportAllocs()
	req := reqBytes(`GET / HTTP/1.0
Host: golang.org
Accept: text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8
User-Agent: Mozilla/5.0 (Macintosh; Intel Mac OS X 10_8_2) AppleWebKit/537.17 (KHTML, like Gecko) Chrome/24.0.1312.52 Safari/537.17
Accept-Encoding: gzip,deflate,sdch
Accept-Language: en-US,en;q=0.8
Accept-Charset: ISO-8859-1,utf-8;q=0.7,*;q=0.3
`)
	res := []byte("Hello world!\n")

	conn := &testConn{
		// testConn.Close will not push into the channel
		// if it's full.
		closec: make(chan bool, 1),
	}
	handler := HandlerFunc(func(rw ResponseWriter, r *Request) {
		rw.Header().Set(ContentType, "text/html; charset=utf-8")
		rw.Write(res)
	})
	ln := new(oneConnListener)
	for i := 0; i < b.N; i++ {
		conn.readBuf.Reset()
		conn.writeBuf.Reset()
		conn.readBuf.Write(req)
		ln.conn = conn
		Serve(ln, handler)
		<-conn.closec
	}
}

func BenchmarkServerFakeConnWithKeepAlive(b *testing.B) {
	b.ReportAllocs()

	req := reqBytes(`GET / HTTP/1.1
Host: golang.org
Accept: text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8
User-Agent: Mozilla/5.0 (Macintosh; Intel Mac OS X 10_8_2) AppleWebKit/537.17 (KHTML, like Gecko) Chrome/24.0.1312.52 Safari/537.17
Accept-Encoding: gzip,deflate,sdch
Accept-Language: en-US,en;q=0.8
Accept-Charset: ISO-8859-1,utf-8;q=0.7,*;q=0.3
`)
	res := []byte("Hello world!\n")

	conn := &rwTestConn{
		Reader: &repeatReader{content: req, count: b.N},
		Writer: ioutil.Discard,
		closec: make(chan bool, 1),
	}
	handled := 0
	handler := HandlerFunc(func(rw ResponseWriter, r *Request) {
		handled++
		rw.Header().Set(ContentType, "text/html; charset=utf-8")
		rw.Write(res)
	})
	ln := &oneConnListener{conn: conn}
	go Serve(ln, handler)
	<-conn.closec
	if b.N != handled {
		b.Errorf("b.N=%d but handled %d", b.N, handled)
	}
}

// same as above, but representing the most simple possible request
// and handler. Notably: the handler does not call rw.Header().
func BenchmarkServerFakeConnWithKeepAliveLite(b *testing.B) {
	b.ReportAllocs()

	req := reqBytes(`GET / HTTP/1.1
Host: golang.org
`)
	res := []byte("Hello world!\n")

	conn := &rwTestConn{
		Reader: &repeatReader{content: req, count: b.N},
		Writer: ioutil.Discard,
		closec: make(chan bool, 1),
	}
	handled := 0
	handler := HandlerFunc(func(rw ResponseWriter, r *Request) {
		handled++
		rw.Write(res)
	})
	ln := &oneConnListener{conn: conn}
	go Serve(ln, handler)
	<-conn.closec
	if b.N != handled {
		b.Errorf("b.N=%d but handled %d", b.N, handled)
	}
}

// Both Content-Type and Content-Length set. Should be no buffering.
func BenchmarkServerHandlerTypeLen(b *testing.B) {
	benchmarkHandler(b, HandlerFunc(func(w ResponseWriter, r *Request) {
		w.Header().Set(ContentType, "text/html")
		w.Header().Set(ContentLength, strconv.Itoa(len(response)))
		w.Write(response)
	}))
}

// A Content-Type is set, but no length. No sniffing, but will count the Content-Length.
func BenchmarkServerHandlerNoLen(b *testing.B) {
	benchmarkHandler(b, HandlerFunc(func(w ResponseWriter, r *Request) {
		w.Header().Set(ContentType, "text/html")
		w.Write(response)
	}))
}

// A Content-Length is set, but the Content-Type will be sniffed.
func BenchmarkServerHandlerNoType(b *testing.B) {
	benchmarkHandler(b, HandlerFunc(func(w ResponseWriter, r *Request) {
		w.Header().Set(ContentLength, strconv.Itoa(len(response)))
		w.Write(response)
	}))
}

// Neither a Content-Type or Content-Length, so sniffed and counted.
func BenchmarkServerHandlerNoHeader(b *testing.B) {
	benchmarkHandler(b, HandlerFunc(func(w ResponseWriter, r *Request) {
		w.Write(response)
	}))
}

func benchmarkHandler(b *testing.B, h Handler) {
	b.ReportAllocs()
	req := reqBytes(`GET / HTTP/1.1
Host: golang.org
`)
	conn := &rwTestConn{
		Reader: &repeatReader{content: req, count: b.N},
		Writer: ioutil.Discard,
		closec: make(chan bool, 1),
	}
	handled := 0
	handler := HandlerFunc(func(rw ResponseWriter, r *Request) {
		handled++
		h.ServeHTTP(rw, r)
	})
	ln := &oneConnListener{conn: conn}
	go Serve(ln, handler)
	<-conn.closec
	if b.N != handled {
		b.Errorf("b.N=%d but handled %d", b.N, handled)
	}
}

func BenchmarkServerHijack(b *testing.B) {
	b.ReportAllocs()
	req := reqBytes(`GET / HTTP/1.1
Host: golang.org
`)
	h := HandlerFunc(func(w ResponseWriter, r *Request) {
		conn, _, err := w.(Hijacker).Hijack()
		if err != nil {
			panic(err)
		}
		conn.Close()
	})
	conn := &rwTestConn{
		Writer: ioutil.Discard,
		closec: make(chan bool, 1),
	}
	ln := &oneConnListener{conn: conn}
	for i := 0; i < b.N; i++ {
		conn.Reader = bytes.NewReader(req)
		ln.conn = conn
		Serve(ln, h)
		<-conn.closec
	}
}

func BenchmarkCloseNotifier(b *testing.B) {
	b.ReportAllocs()
	b.StopTimer()
	sawClose := make(chan bool)
	ts := th.NewServer(HandlerFunc(func(rw ResponseWriter, req *Request) {
		<-rw.(CloseNotifier).CloseNotify()
		sawClose <- true
	}))
	defer ts.Close()
	tot := time.NewTimer(5 * time.Second)
	defer tot.Stop()
	b.StartTimer()
	for i := 0; i < b.N; i++ {
		conn, err := net.Dial("tcp", ts.Listener.Addr().String())
		if err != nil {
			b.Fatalf("error dialing: %v", err)
		}
		_, err = fmt.Fprintf(conn, "GET / HTTP/1.1\r\nConnection: keep-alive\r\nHost: foo\r\n\r\n")
		if err != nil {
			b.Fatal(err)
		}
		conn.Close()
		tot.Reset(5 * time.Second)
		select {
		case <-sawClose:
		case <-tot.C:
			b.Fatal("timeout")
		}
	}
	b.StopTimer()
}

// A benchmark for profiling the client without the HTTP server code.
// The server code runs in a subprocess.
func BenchmarkClient(b *testing.B) {
	b.ReportAllocs()
	b.StopTimer()
	defer afterTest(b)

	var data = []byte("Hello world.\n")
	if server := os.Getenv("TEST_BENCH_SERVER"); server != "" {
		b.Log("TEST_BENCH_SERVER")
		// Server process mode.
		port := os.Getenv("TEST_BENCH_SERVER_PORT") // can be set by user
		if port == "" {
			port = "0"
		}
		ln, err := net.Listen("tcp", "localhost:"+port)
		if err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			os.Exit(1)
		}
		fmt.Println(ln.Addr().String())
		mux.HandleFunc("/", func(w ResponseWriter, r *Request) {
			r.ParseForm()
			if r.Form.Get("stop") != "" {
				os.Exit(0)
			}
			w.Header().Set(ContentType, "text/html; charset=utf-8")
			w.Write(data)
		})
		var srv Server
		// @comment : since we have disabled the link between server and default mux, we have to provide it
		srv.Handler = mux.DefaultServeMux
		log.Fatal(srv.Serve(ln))
	}

	// Start server process.
	cmd := exec.Command(os.Args[0], "-test.run=XXXX", "-test.bench=BenchmarkClient$")
	cmd.Env = append(os.Environ(), "TEST_BENCH_SERVER=yes")
	cmd.Stderr = os.Stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		b.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		b.Fatalf("subprocess failed to start: %v", err)
	}
	defer cmd.Process.Kill()

	// Wait for the server in the child process to respond and tell us
	// its listening address, once it's started listening:
	timer := time.AfterFunc(10*time.Second, func() {
		cmd.Process.Kill()
	})
	defer timer.Stop()
	bs := bufio.NewScanner(stdout)
	if !bs.Scan() {
		b.Fatalf("failed to read listening URL from child: %v", bs.Err())
	}
	benchUrl := HttpUrlPrefix + strings.TrimSpace(bs.Text()) + "/"
	timer.Stop()
	if _, err := getNoBody(benchUrl); err != nil {
		b.Fatalf("initial probe of child process failed: %v", err)
	}

	done := make(chan error)
	go func() {
		done <- cmd.Wait()
	}()

	// Do b.N requests to the server.
	b.StartTimer()
	for i := 0; i < b.N; i++ {
		res, err := cli.Get(benchUrl)
		if err != nil {
			b.Fatalf("Get: %v", err)
		}
		body, err := ioutil.ReadAll(res.Body)
		res.CloseBody()
		if err != nil {
			b.Fatalf("ReadAll: %v", err)
		}
		if !bytes.Equal(body, data) {
			b.Fatalf("Got body: %q", body)
		}
	}
	b.StopTimer()

	// Instruct server process to stop.
	getNoBody(benchUrl + "?stop=yes")
	select {
	case err := <-done:
		if err != nil {
			b.Fatalf("subprocess failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		b.Fatalf("subprocess did not stop")
	}
}

func BenchmarkResponseStatusLine(b *testing.B) {
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		bw := bufio.NewWriter(ioutil.Discard)
		var buf3 [3]byte
		for pb.Next() {
			WriteStatusLine(bw, true, 200, buf3[:])
		}
	})
}

func BenchmarkClientServer(b *testing.B) {
	b.ReportAllocs()
	b.StopTimer()
	ts := th.NewServer(HandlerFunc(func(rw ResponseWriter, r *Request) {
		fmt.Fprintf(rw, "Hello world.\n")
	}))
	defer ts.Close()
	b.StartTimer()

	for i := 0; i < b.N; i++ {
		res, err := cli.Get(ts.URL)
		if err != nil {
			b.Fatal("Get:", err)
		}
		all, err := ioutil.ReadAll(res.Body)
		res.CloseBody()
		if err != nil {
			b.Fatal("ReadAll:", err)
		}
		body := string(all)
		if body != "Hello world.\n" {
			b.Fatal("Got body:", body)
		}
	}

	b.StopTimer()
}
