/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package http

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"sync"

	"github.com/badu/http/mime"

	"github.com/badu/http/hdr"
	"github.com/badu/http/url"
)

const (
	defaultMaxMemory = 32 << 20 // 32 MB

	// NOTE: This is not intended to reflect the actual Go version being used.
	// It was changed at the time of Go 1.1 release because the former User-Agent
	// had ended up on a blacklist for some intrusion detection systems.
	// See https://codereview.appspot.com/7532043.
	DefaultUserAgent = "Go-http-client/1.1"
)

var (
	// ErrMissingFile is returned by FormFile when the provided file field name
	// is either not present in the request or not a file field.

	ErrMissingFile = errors.New("http: no such file")

	// ErrUnexpectedTrailer is returned by the Transport when a server
	// replies with a Trailer header, but without a chunked reply.
	ErrUnexpectedTrailer = errors.New("trailer header without chunked transfer encoding")

	// ErrMissingBoundary is returned by Request.MultipartReader when the
	// request's Content-Type does not include a "boundary" parameter.
	ErrMissingBoundary = errors.New("no multipart boundary param in Content-Type")

	// ErrNotMultipart is returned by Request.MultipartReader when the
	// request's Content-Type is not multipart/form-data.
	ErrNotMultipart = errors.New("request Content-Type isn't multipart/form-data")

	// Headers that Request.Write handles itself and should be skipped.
	reqWriteExcludeHeader = map[string]bool{
		hdr.Host:             true, // not in Header map anyway
		hdr.UserAgent:        true,
		hdr.ContentLength:    true,
		hdr.TransferEncoding: true,
		hdr.Trailer:          true,
	}

	// ErrNoCookie is returned by Request's Cookie method when a cookie is not found.
	ErrNoCookie = errors.New("http: named cookie not present")

	// multipartByReader is a sentinel value.
	// Its presence in Request.MultipartForm indicates that parsing of the request
	// body has been handed off to a MultipartReader instead of ParseMultipartFrom.
	multipartByReader = &mime.Form{
		Value: make(map[string][]string),
		File:  make(map[string][]*mime.FileHeader),
	}

	// ErrMissingHost is returned by Write when there is no Host or URL present in
	// the Request.
	ErrMissingHost = errors.New("http: Request.Write on Request with no Host or URL set")

	headerReaderPool sync.Pool
)

type (
	badStringError struct {
		what string
		str  string
	}

	// A Request represents an HTTP request received by a server
	// or to be sent by a client.
	//
	// The field semantics differ slightly between client and server
	// usage. In addition to the notes on the fields below, see the
	// documentation for Request.Write and RoundTripper.
	Request struct {
		// Method specifies the HTTP method (GET, POST, PUT, etc.).
		// For client requests an empty string means GET.
		Method string

		// URL specifies either the URI being requested (for server
		// requests) or the URL to access (for client requests).
		//
		// For server requests the URL is parsed from the URI
		// supplied on the Request-Line as stored in RequestURI.  For
		// most requests, fields other than Path and RawQuery will be
		// empty. (See RFC 2616, Section 5.1.2)
		//
		// For client requests, the URL's Host specifies the server to
		// connect to, while the Request's Host field optionally
		// specifies the Host header value to send in the HTTP
		// request.
		URL *url.URL

		// The protocol version for incoming server requests.
		//
		// For client requests these fields are ignored. The HTTP
		// client code always uses HTTP/1.1 .
		// See the docs on Transport for details.
		Proto      string // "HTTP/1.0"
		ProtoMajor int    // 1
		ProtoMinor int    // 0

		// Header contains the request header fields either received
		// by the server or to be sent by the client.
		//
		// If a server received a request with header lines,
		//
		//	Host: example.com
		//	accept-encoding: gzip, deflate
		//	Accept-Language: en-us
		//	fOO: Bar
		//	foo: two
		//
		// then
		//
		//	Header = map[string][]string{
		//		"Accept-Encoding": {"gzip, deflate"},
		//		"Accept-Language": {"en-us"},
		//		"Foo": {"Bar", "two"},
		//	}
		//
		// For incoming requests, the Host header is promoted to the
		// Request.Host field and removed from the Header map.
		//
		// HTTP defines that header names are case-insensitive. The
		// request parser implements this by using CanonicalHeaderKey,
		// making the first character and any characters following a
		// hyphen uppercase and the rest lowercase.
		//
		// For client requests, certain headers such as Content-Length
		// and Connection are automatically written when needed and
		// values in Header may be ignored. See the documentation
		// for the Request.Write method.
		Header hdr.Header

		// Body is the request's body.
		//
		// For client requests a nil body means the request has no
		// body, such as a GET request. The HTTP Client's Transport
		// is responsible for calling the Close method.
		//
		// For server requests the Request Body is always non-nil
		// but will return EOF immediately when no body is present.
		// The Server will close the request body. The ServeHTTP
		// Handler does not need to.
		Body io.ReadCloser

		// GetBody defines an optional func to return a new copy of
		// Body. It is used for client requests when a redirect requires
		// reading the body more than once. Use of GetBody still
		// requires setting Body.
		//
		// For server requests it is unused.
		GetBody func() (io.ReadCloser, error)

		// ContentLength records the length of the associated content.
		// The value -1 indicates that the length is unknown.
		// Values >= 0 indicate that the given number of bytes may
		// be read from Body.
		// For client requests, a value of 0 with a non-nil Body is
		// also treated as unknown.
		ContentLength int64

		// TransferEncoding lists the transfer encodings from outermost to
		// innermost. An empty list denotes the "identity" encoding.
		// TransferEncoding can usually be ignored; chunked encoding is
		// automatically added and removed as necessary when sending and
		// receiving requests.
		TransferEncoding []string

		// Close indicates whether to close the connection after
		// replying to this request (for servers) or after sending this
		// request and reading its response (for clients).
		//
		// For server requests, the HTTP server handles this automatically
		// and this field is not needed by Handlers.
		//
		// For client requests, setting this field prevents re-use of
		// TCP connections between requests to the same hosts, as if
		// Transport.DisableKeepAlives were set.
		Close bool

		// For server requests Host specifies the host on which the
		// URL is sought. Per RFC 2616, this is either the value of
		// the "Host" header or the host name given in the URL itself.
		// It may be of the form "host:port". For international domain
		// names, Host may be in Punycode or Unicode form. Use
		// golang.org/x/net/idna to convert it to either format if
		// needed.
		//
		// For client requests Host optionally overrides the Host
		// header to send. If empty, the Request.Write method uses
		// the value of URL.Host. Host may contain an international
		// domain name.
		Host string

		// Form contains the parsed form data, including both the URL
		// field's query parameters and the POST or PUT form data.
		// This field is only available after ParseForm is called.
		// The HTTP client ignores Form and uses Body instead.
		Form url.Values

		// PostForm contains the parsed form data from POST, PATCH,
		// or PUT body parameters.
		//
		// This field is only available after ParseForm is called.
		// The HTTP client ignores PostForm and uses Body instead.
		PostForm url.Values

		// MultipartForm is the parsed multipart form, including file uploads.
		// This field is only available after ParseMultipartForm is called.
		// The HTTP client ignores MultipartForm and uses Body instead.
		MultipartForm *mime.Form

		// Trailer specifies additional headers that are sent after the request
		// body.
		//
		// For server requests the Trailer map initially contains only the
		// trailer keys, with nil values. (The client declares which trailers it
		// will later send.)  While the handler is reading from Body, it must
		// not reference Trailer. After reading from Body returns EOF, Trailer
		// can be read again and will contain non-nil values, if they were sent
		// by the client.
		//
		// For client requests Trailer must be initialized to a map containing
		// the trailer keys to later send. The values may be nil or their final
		// values. The ContentLength must be 0 or -1, to send a chunked request.
		// After the HTTP request is sent the map values can be updated while
		// the request body is read. Once the body returns EOF, the caller must
		// not mutate Trailer.
		//
		// Few HTTP clients, servers, or proxies support HTTP trailers.
		Trailer hdr.Header

		// RemoteAddr allows HTTP servers and other software to record
		// the network address that sent the request, usually for
		// logging. This field is not filled in by ReadRequest and
		// has no defined format. The HTTP server in this package
		// sets RemoteAddr to an "IP:port" address before invoking a
		// handler.
		// This field is ignored by the HTTP client.
		RemoteAddr string

		// RequestURI is the unmodified Request-URI of the
		// Request-Line (RFC 2616, Section 5.1) as sent by the client
		// to a server. Usually the URL field should be used instead.
		// It is an error to set this field in an HTTP client request.
		RequestURI string

		// TLS allows HTTP servers and other software to record
		// information about the TLS connection on which the request
		// was received. This field is not filled in by ReadRequest.
		// The HTTP server in this package sets the field for
		// TLS-enabled connections before invoking a handler;
		// otherwise it leaves the field nil.
		// This field is ignored by the HTTP client.
		TLS *tls.ConnectionState

		// Response is the redirect response which caused this request
		// to be created. This field is only populated during client
		// redirects.
		//TODO : @badu - see that the only place where server uses Response struct is here! In case you wonder
		Response *Response

		// ctx is either the client or server context. It should only
		// be modified via copying the whole Request using WithContext.
		// It is unexported to prevent people from using Context wrong
		// and mutating the contexts held by callers of the same request.
		ctx context.Context
	}
	// RequestBodyReadError wraps an error from (*Request).write to indicate
	// that the error came from a Read call on the Request.Body.
	// This error type should not escape the net/http package to users.
	RequestBodyReadError struct {
		error
	}
	// The server code and client code both use
	// maxBytesReader. This "requestTooLarge" check is
	// only used by the server code. To prevent binaries
	// which only using the HTTP Client code (such as
	// cmd/go) from also linking in the HTTP server, don't
	// use a static type assertion to the server
	// "*response" type. Check this interface instead:
	requestTooLarger interface {
		requestTooLarge()
	}

	maxBytesReader struct {
		respWriter     ResponseWriter
		readCloser     io.ReadCloser // underlying reader
		bytesRemaining int64         // max bytes remaining
		err            error         // sticky error
	}
)
