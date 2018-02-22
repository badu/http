/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package http

const (

	//Headers
	Accept                  = "Accept"
	AcceptCharset           = "Accept-Charset"
	AcceptEncoding          = "Accept-Encoding"
	AcceptLanguage          = "Accept-Language"
	AcceptRanges            = "Accept-Ranges"
	Authorization           = "Authorization"
	CacheControl            = "Cache-Control"
	Cc                      = "Cc"
	Connection              = "Connection"
	ContentEncoding         = "Content-Encoding"
	ContentId               = "Content-Id"
	ContentLanguage         = "Content-Language"
	ContentLength           = "Content-Length"
	ContentRange            = "Content-Range"
	ContentTransferEncoding = "Content-Transfer-Encoding"
	ContentType             = "Content-Type"
	CookieHeader            = "Cookie"
	Date                    = "Date"
	DkimSignature           = "Dkim-Signature"
	Etag                    = "Etag"
	Expires                 = "Expires"
	Expect                  = "Expect"
	From                    = "From"
	Host                    = "Host"
	IfModifiedSince         = "If-Modified-Since"
	IfNoneMatch             = "If-None-Match"
	InReplyTo               = "In-Reply-To"
	LastModified            = "Last-Modified"
	Location                = "Location"
	MessageId               = "Message-Id"
	MimeVersion             = "Mime-Version"
	Pragma                  = "Pragma"
	Received                = "Received"
	Referer                 = "Referer"
	ReturnPath              = "Return-Path"
	ServerHeader            = "Server"
	SetCookieHeader         = "Set-Cookie"
	Subject                 = "Subject"
	TransferEncoding        = "Transfer-Encoding"
	To                      = "To"
	Trailer                 = "Trailer"
	UpgradeHeader           = "Upgrade"
	UserAgent               = "User-Agent"
	Via                     = "Via"
	XForwardedFor           = "X-Forwarded-For"
	XImforwards             = "X-Imforwards"
	XPoweredBy              = "X-Powered-By"

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
	HTTP     = "http"
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
	TrailerPrefix = "Trailer:"

	HttpUrlPrefix  = "http://"
	HttpsUrlPrefix = "https://"

	FormData    = "multipart/form-data"
	OctetStream = "application/octet-stream"
	XFormData   = "application/x-www-form-urlencoded"
)
