/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package tport

import (
	"strings"

	. "github.com/badu/http"
	"github.com/badu/http/url"
)

// proxyAuth returns the Proxy-Authorization header to set
// on requests, if applicable.
func (m *connectMethod) proxyAuth() string {
	if m.proxyURL == nil {
		return ""
	}
	if u := m.proxyURL.User; u != nil {
		username := u.Username()
		password, _ := u.Password()
		return "Basic " + url.BasicAuth(username, password)
	}
	return ""
}

func (m *connectMethod) key() connectMethodKey {
	proxyStr := ""
	targetAddr := m.targetAddr
	if m.proxyURL != nil {
		proxyStr = m.proxyURL.String()
		//@comment : was `if strings.HasPrefix(m.proxyURL.Scheme, HTTP) && m.targetScheme == HTTP {`
		if len(m.proxyURL.Scheme) >= len(HTTP) && m.proxyURL.Scheme[0:len(HTTP)] == HTTP && m.targetScheme == HTTP {
			targetAddr = ""
		}
	}
	return connectMethodKey{
		proxy:  proxyStr,
		scheme: m.targetScheme,
		addr:   targetAddr,
	}
}

//TODO : @badu - exported because tests
func (m *connectMethod) Key() connectMethodKey {
	return m.key()
}

//TODO : @badu - exported because tests / "exported function with unexported return type"
func NewConnectMethod(proxy *url.URL, scheme, addr string) connectMethod {
	return connectMethod{proxy, scheme, addr}
}

// addr returns the first hop "host:port" to which we need to TCP connect.
func (m *connectMethod) addr() string {
	if m.proxyURL != nil {
		return canonicalAddr(m.proxyURL)
	}
	return m.targetAddr
}

// tlsHost returns the host name to match against the peer's
// TLS certificate.
func (m *connectMethod) tlsHost() string {
	h := m.targetAddr
	if hasPort(h) {
		h = h[:strings.LastIndex(h, ":")]
	}
	return h
}
