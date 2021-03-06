/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package tests

import (
	"os"
	"testing"

	. "github.com/badu/http"
	. "github.com/badu/http/tport"
	"github.com/badu/http/url"
)

func TestUseProxy(t *testing.T) {
	ResetProxyEnv()
	os.Setenv("NO_PROXY", "foobar.com, .barbaz.net")

	var UseProxyTests = []struct {
		host  string
		match bool
	}{
		// Never proxy localhost:
		{"localhost", false},
		{"127.0.0.1", false},
		{"127.0.0.2", false},
		{"[::1]", false},
		{"[::2]", true}, // not a loopback address

		{"barbaz.net", false},     // match as .barbaz.net
		{"foobar.com", false},     // have a port but match
		{"foofoobar.com", true},   // not match as a part of foobar.com
		{"baz.com", true},         // not match as a part of barbaz.com
		{"localhost.net", true},   // not match as suffix of address
		{"local.localhost", true}, // not match as prefix as address
		{"barbarbaz.net", true},   // not match because NO_PROXY have a '.'
		{"www.foobar.com", false}, // match because NO_PROXY includes "foobar.com"
	}

	for _, test := range UseProxyTests {
		if UseProxy(test.host+":80") != test.match {
			t.Errorf("useProxy(%v) = %v, want %v", test.host, !test.match, test.match)
		}
	}
}

func TestCacheKeys(t *testing.T) {

	var cacheKeysTests = []struct {
		proxy  string
		scheme string
		addr   string
		key    string
	}{
		{"", HTTP, "foo.com", "|http|foo.com"},
		{"", HTTPS, "foo.com", "|https|foo.com"},
		{"http://foo.com", HTTP, "foo.com", "http://foo.com|http|"},
		{"http://foo.com", HTTPS, "foo.com", "http://foo.com|https|foo.com"},
	}

	for _, tt := range cacheKeysTests {
		var proxy *url.URL
		if tt.proxy != "" {
			u, err := url.Parse(tt.proxy)
			if err != nil {
				t.Fatal(err)
			}
			proxy = u
		}
		cm := NewConnectMethod(proxy, tt.scheme, tt.addr)
		if got := cm.Key().String(); got != tt.key {
			t.Fatalf("{%q, %q, %q} cache key = %q; want %q", tt.proxy, tt.scheme, tt.addr, got, tt.key)
		}
	}
}

func TestInvalidNoProxy(t *testing.T) {
	ResetProxyEnv()
	os.Setenv("NO_PROXY", ":1")
	UseProxy("example.com:80") // should not panic
}
