/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package th

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log"
	"net"
	"os"
	"time"

	. "http"
	"http/cli"
	. "http/tport"
)

// Start starts a server from NewUnstartedServer.
func (s *TServer) Start() {
	if s.URL != "" {
		panic("Server already started")
	}
	if s.client == nil {
		s.client = &cli.Client{Transport: &Transport{}}
	}
	s.URL = HttpUrlPrefix + s.Listener.Addr().String()
	s.wrap()
	s.goServe()
	if *serve != "" {
		fmt.Fprintln(os.Stderr, "httptest: serving on", s.URL)
		select {}
	}
}

// StartTLS starts TLS on a server from NewUnstartedServer.
func (s *TServer) StartTLS() {
	if s.URL != "" {
		panic("Server already started")
	}
	if s.client == nil {
		s.client = &cli.Client{Transport: &Transport{}}
	}
	cert, err := tls.X509KeyPair(LocalhostCert, LocalhostKey)
	if err != nil {
		panic(fmt.Sprintf("httptest: NewTLSServer: %v", err))
	}

	existingConfig := s.TLS
	if existingConfig != nil {
		s.TLS = existingConfig.Clone()
	} else {
		s.TLS = new(tls.Config)
	}
	if s.TLS.NextProtos == nil {
		s.TLS.NextProtos = []string{"http/1.1"}
	}
	if len(s.TLS.Certificates) == 0 {
		s.TLS.Certificates = []tls.Certificate{cert}
	}
	s.certificate, err = x509.ParseCertificate(s.TLS.Certificates[0].Certificate[0])
	if err != nil {
		panic(fmt.Sprintf("httptest: NewTLSServer: %v", err))
	}
	certpool := x509.NewCertPool()
	certpool.AddCert(s.certificate)
	s.client.Transport = &Transport{
		TLSClientConfig: &tls.Config{
			RootCAs: certpool,
		},
	}
	s.Listener = tls.NewListener(s.Listener, s.TLS)
	s.URL = HttpsUrlPrefix + s.Listener.Addr().String()
	s.wrap()
	s.goServe()
}

// Close shuts down the server and blocks until all outstanding
// requests on this server have completed.
func (s *TServer) Close() {
	s.mu.Lock()
	if !s.closed {
		s.closed = true
		s.Listener.Close()
		s.Config.SetKeepAlivesEnabled(false)
		for c, st := range s.conns {
			// Force-close any idle connections (those between
			// requests) and new connections (those which connected
			// but never sent a request). StateNew connections are
			// super rare and have only been seen (in
			// previously-flaky tests) in the case of
			// socket-late-binding races from the http Client
			// dialing this server and then getting an idle
			// connection before the dial completed. There is thus
			// a connected connection in StateNew with no
			// associated Request. We only close StateIdle and
			// StateNew because they're not doing anything. It's
			// possible StateNew is about to do something in a few
			// milliseconds, but a previous CL to check again in a
			// few milliseconds wasn't liked (early versions of
			// https://golang.org/cl/15151) so now we just
			// forcefully close StateNew. The docs for Server.Close say
			// we wait for "outstanding requests", so we don't close things
			// in StateActive.
			if st == StateIdle || st == StateNew {
				s.closeConn(c)
			}
		}
		// If this server doesn't shut down in 5 seconds, tell the user why.
		t := time.AfterFunc(5*time.Second, s.logCloseHangDebugInfo)
		defer t.Stop()
	}
	s.mu.Unlock()

	// Not part of httptest.Server's correctness, but assume most
	// users of httptest.Server will be using the standard
	// transport, so help them out and close any idle connections for them.
	if t, ok := DefaultTransport.(closeIdleTransport); ok {
		t.CloseIdleConnections()
	}

	// Also close the client idle connections.
	if s.client != nil {
		if t, ok := s.client.Transport.(closeIdleTransport); ok {
			t.CloseIdleConnections()
		}
	}

	s.wg.Wait()
}

func (s *TServer) logCloseHangDebugInfo() {
	s.mu.Lock()
	defer s.mu.Unlock()
	var buf bytes.Buffer
	buf.WriteString("httptest.Server blocked in Close after 5 seconds, waiting for connections:\n")
	for c, st := range s.conns {
		fmt.Fprintf(&buf, "  %T %p %v in state %v\n", c, c, c.RemoteAddr(), st)
	}
	log.Print(buf.String())
}

// CloseClientConnections closes any open HTTP connections to the test Server.
func (s *TServer) CloseClientConnections() {
	s.mu.Lock()
	nconn := len(s.conns)
	ch := make(chan struct{}, nconn)
	for c := range s.conns {
		go s.closeConnChan(c, ch)
	}
	s.mu.Unlock()

	// Wait for outstanding closes to finish.
	//
	// Out of paranoia for making a late change in Go 1.6, we
	// bound how long this can wait, since golang.org/issue/14291
	// isn't fully understood yet. At least this should only be used
	// in tests.
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for i := 0; i < nconn; i++ {
		select {
		case <-ch:
		case <-timer.C:
			// Too slow. Give up.
			return
		}
	}
}

// Certificate returns the certificate used by the server, or nil if
// the server doesn't use TLS.
func (s *TServer) Certificate() *x509.Certificate {
	return s.certificate
}

// Client returns an HTTP client configured for making requests to the server.
// It is configured to trust the server's TLS test certificate and will
// close its idle connections on Server.Close.
func (s *TServer) Client() *cli.Client {
	return s.client
}

func (s *TServer) goServe() {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.Config.Serve(s.Listener)
	}()
}

// wrap installs the connection state-tracking hook to know which
// connections are idle.
func (s *TServer) wrap() {
	oldHook := s.Config.ConnState
	s.Config.ConnState = func(c net.Conn, cs ConnState) {
		s.mu.Lock()
		defer s.mu.Unlock()
		switch cs {
		case StateNew:
			s.wg.Add(1)
			if _, exists := s.conns[c]; exists {
				panic("invalid state transition")
			}
			if s.conns == nil {
				s.conns = make(map[net.Conn]ConnState)
			}
			s.conns[c] = cs
			if s.closed {
				// Probably just a socket-late-binding dial from
				// the default transport that lost the race (and
				// thus this connection is now idle and will
				// never be used).
				s.closeConn(c)
			}
		case StateActive:
			if oldState, ok := s.conns[c]; ok {
				if oldState != StateNew && oldState != StateIdle {
					panic("invalid state transition")
				}
				s.conns[c] = cs
			}
		case StateIdle:
			if oldState, ok := s.conns[c]; ok {
				if oldState != StateActive {
					panic("invalid state transition")
				}
				s.conns[c] = cs
			}
			if s.closed {
				s.closeConn(c)
			}
		case StateHijacked, StateClosed:
			s.forgetConn(c)
		}
		if oldHook != nil {
			oldHook(c, cs)
		}
	}
}

// closeConn closes c.
// s.mu must be held.
func (s *TServer) closeConn(c net.Conn) { s.closeConnChan(c, nil) }

// closeConnChan is like closeConn, but takes an optional channel to receive a value
// when the goroutine closing c is done.
func (s *TServer) closeConnChan(c net.Conn, done chan<- struct{}) {
	c.Close()
	if done != nil {
		done <- struct{}{}
	}
}

// forgetConn removes c from the set of tracked conns and decrements it from the
// waitgroup, unless it was previously removed.
// s.mu must be held.
func (s *TServer) forgetConn(c net.Conn) {
	if _, ok := s.conns[c]; ok {
		delete(s.conns, c)
		s.wg.Done()
	}
}
