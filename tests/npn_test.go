/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package tests

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"io/ioutil"
	"strings"
	"testing"

	. "github.com/badu/http"
	"github.com/badu/http/cli"
	"github.com/badu/http/hdr"
	"github.com/badu/http/th"
	. "github.com/badu/http/tport"
)

type http09Writer struct {
	io.Writer
	h hdr.Header
}

func (w http09Writer) Header() hdr.Header { return w.h }

func (w http09Writer) WriteHeader(int) {}

func TestNextProtoUpgrade(t *testing.T) {
	setParallel(t)
	defer afterTest(t)
	ts := th.NewUnstartedServer(HandlerFunc(func(w ResponseWriter, r *Request) {
		fmt.Fprintf(w, "path=%s,proto=", r.URL.Path)
		if r.TLS != nil {
			w.Write([]byte(r.TLS.NegotiatedProtocol))
		}
		if r.RemoteAddr == "" {
			t.Error("request with no RemoteAddr")
		}
		if r.Body == nil {
			t.Errorf("request with nil Body")
		}
	}))
	ts.TLS = &tls.Config{
		NextProtos: []string{"unhandled-proto", "tls-0.9"},
	}
	ts.Server.TLSNextProto = map[string]TLSConHandler{
		"tls-0.9": func(conn *tls.Conn, h Handler) {
			// handleTLSProtocol09 implements the HTTP/0.9 protocol over TLS, for the TestNextProtoUpgrade test.
			br := bufio.NewReader(conn)
			line, err := br.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimSpace(line)
			path := strings.TrimPrefix(line, "GET ")
			if path == line {
				return
			}
			req, _ := NewRequest(GET, path, nil)
			req.Proto = "HTTP/0.9"
			req.ProtoMajor = 0
			req.ProtoMinor = 9
			rw := &http09Writer{conn, make(hdr.Header)}
			h.ServeHTTP(rw, req)
		},
	}
	ts.StartTLS()
	defer ts.Close()

	// Normal request, without NPN.
	{
		c := ts.Client()
		res, err := c.Get(ts.URL)
		if err != nil {
			t.Fatal(err)
		}
		body, err := ioutil.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		if want := "path=/,proto="; string(body) != want {
			t.Errorf("plain request = %q; want %q", body, want)
		}
	}

	// Request to an advertised but unhandled NPN protocol.
	// Server will hang up.
	{
		certPool := x509.NewCertPool()
		certPool.AddCert(ts.Certificate())
		tr := &Transport{
			TLSClientConfig: &tls.Config{
				RootCAs:    certPool,
				NextProtos: []string{"unhandled-proto"},
			},
		}
		defer tr.CloseIdleConnections()
		c := &cli.Client{
			Transport: tr,
		}
		res, err := c.Get(ts.URL)
		if err == nil {
			defer res.CloseBody()
			var buf bytes.Buffer
			res.Write(&buf)
			t.Errorf("expected error on unhandled-proto request; got: %s", buf.Bytes())
		}
	}

	// Request using the "tls-0.9" protocol, which we register here.
	// It is HTTP/0.9 over TLS.
	{
		c := ts.Client()
		tlsConfig := c.Transport.(*Transport).TLSClientConfig
		tlsConfig.NextProtos = []string{"tls-0.9"}
		conn, err := tls.Dial("tcp", ts.Listener.Addr().String(), tlsConfig)
		if err != nil {
			t.Fatal(err)
		}
		conn.Write([]byte("GET /foo\n"))
		body, err := ioutil.ReadAll(conn)
		if err != nil {
			t.Fatal(err)
		}
		if want := "path=/foo,proto=tls-0.9"; string(body) != want {
			t.Errorf("plain request = %q; want %q", body, want)
		}
	}
}
