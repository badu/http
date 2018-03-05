/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package http

const (
	GET      = "GET"
	POST     = "POST"
	CONNECT  = "CONNECT"
	DELETE   = "DELETE"
	HEAD     = "HEAD"
	OPTIONS  = "OPTIONS"
	PUT      = "PUT"
	PROPFIND = "PROPFIND"
	SEARCH   = "SEARCH"
	PATCH    = "PATCH"
	TRACE    = "TRACE"
	HTTP     = "http" // ATTN : do not change - will break
	HTTPS    = "https"
	SOCK5    = "socks5"
	HTTP1_1  = "HTTP/1.1"
	HTTP1_0  = "HTTP/1.0"

	ProxyConnection    = "Proxy-Connection" // non-standard but still sent by libcurl and rejected by e.g. google
	KeepAlive          = "Keep-Alive"
	ProxyAuthenticate  = "Proxy-Authenticate"
	ProxyAuthorization = "Proxy-Authorization"
	Te                 = "Te" // canonicalized version of "TE"
	// TrailerPrefix is a magic prefix for ResponseWriter.Header map keys
	// that, if present, signals that the map entry is actually for
	// the response trailers, and not the response headers. The prefix
	// is stripped after the ServeHTTP call finishes and the values are
	// sent in the trailers.
	DoClose     = "close"
	DoKeepAlive = "keep-alive"
	DoChunked   = "chunked"
	DoIdentity  = "identity"
	//
	// This mechanism is intended only for trailers that are not known
	// prior to the headers being written. If the set of trailers is fixed
	// or known before the header is written, the normal Go trailers mechanism
	// is preferred:
	//    https://golang.org/pkg/net/http/#ResponseWriter
	//    https://golang.org/pkg/net/http/#example_ResponseWriter_trailers
	TrailerPrefix = "Trailer:" // ATTN : do not change - will break

	HttpUrlPrefix  = "http://"  // ATTN : do not change - will break
	HttpsUrlPrefix = "https://" // ATTN : do not change - will break

	FormData    = "multipart/form-data"
	OctetStream = "application/octet-stream"
	XFormData   = "application/x-www-form-urlencoded"

	errorHeaders = "\r\nContent-Type: text/plain; charset=utf-8\r\nConnection: close\r\n\r\n"
)

var (
	CrLf       = []byte("\r\n")
	Lf         = []byte("\n")
	Cr         = []byte("\n")
	DoubleCrLf = []byte("\r\n\r\n")
)
