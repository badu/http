/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package http

import (
	"bufio"
	"compress/gzip"
	"container/list"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"http/trc"
)

const (
	// DefaultMaxIdleConnsPerHost is the default value of Transport's
	// MaxIdleConnsPerHost.
	DefaultMaxIdleConnsPerHost = 2
)

var (
	// DefaultTransport is the default implementation of Transport and is
	// used by DefaultClient. It establishes network connections as needed
	// and caches them for reuse by subsequent calls. It uses HTTP proxies
	// as directed by the $HTTP_PROXY and $NO_PROXY (or $http_proxy and
	// $no_proxy) environment variables.
	DefaultTransport RoundTripper = &Transport{
		Proxy: ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
			DualStack: true,
		}).DialContext,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}

	// ErrSkipAltProtocol is a sentinel error value defined by Transport.RegisterProtocol.
	ErrSkipAltProtocol = errors.New("net/http: skip alternate protocol")

	//TODO : @badu - all below exposed for tests
	HttpProxyEnv = &envOnce{
		names: []string{"HTTP_PROXY", "http_proxy"},
	}
	HttpsProxyEnv = &envOnce{
		names: []string{"HTTPS_PROXY", "https_proxy"},
	}
	NoProxyEnv = &envOnce{
		names: []string{"NO_PROXY", "no_proxy"},
	}

	// error values for debugging and testing, not seen by users.
	errKeepAlivesDisabled = errors.New("http: putIdleConn: keep alives disabled")
	errConnBroken         = errors.New("http: putIdleConn: connection is in bad state")
	errWantIdle           = errors.New("http: putIdleConn: CloseIdleConnections was called")
	errTooManyIdle        = errors.New("http: putIdleConn: too many idle connections")
	errTooManyIdleHost    = errors.New("http: putIdleConn: too many idle connections for host")
	errCloseIdleConns     = errors.New("http: CloseIdleConnections called")
	errReadLoopExiting    = errors.New("http: persistConn.readLoop exiting")

	//TODO : @badu - exported for tests
	ErrServerClosedIdle = errors.New("http: server closed idle connection")
	errIdleConnTimeout  = errors.New("http: idle connection timeout")
	//errNotCachingH2Conn = errors.New("http: not caching alternate protocol's connections")

	zeroDialer net.Dialer

	errTimeout error = &httpError{err: "net/http: timeout awaiting response headers", timeout: true}

	//TODO : @badu - exported, so tests can access it
	ErrRequestCanceled = errors.New("net/http: request canceled")

	//TODO : @badu - exported, so tests can access it
	ErrRequestCanceledConn = errors.New("net/http: request canceled while waiting for connection") // TODO: unify?

	portMap = map[string]string{
		HTTP:  "80",
		HTTPS: "443",
		SOCK5: "1080",
	}

	errReadOnClosedResBody = errors.New("http: read on closed response body")
)

type (
	// RoundTripper is an interface representing the ability to execute a
	// single HTTP transaction, obtaining the Response for a given Request.
	//
	// A RoundTripper must be safe for concurrent use by multiple
	// goroutines.
	RoundTripper interface {
		// RoundTrip executes a single HTTP transaction, returning
		// a Response for the provided Request.
		//
		// RoundTrip should not attempt to interpret the response. In
		// particular, RoundTrip must return err == nil if it obtained
		// a response, regardless of the response's HTTP status code.
		// A non-nil err should be reserved for failure to obtain a
		// response. Similarly, RoundTrip should not attempt to
		// handle higher-level protocol details such as redirects,
		// authentication, or cookies.
		//
		// RoundTrip should not modify the request, except for
		// consuming and closing the Request's Body.
		//
		// RoundTrip must always close the body, including on errors,
		// but depending on the implementation may do so in a separate
		// goroutine even after RoundTrip returns. This means that
		// callers wanting to reuse the body for subsequent requests
		// must arrange to wait for the Close call before doing so.
		//
		// The Request's URL and Header fields must be initialized.
		RoundTrip(*Request) (*Response, error)
	}

	// Transport is an implementation of RoundTripper that supports HTTP,
	// HTTPS, and HTTP proxies (for either HTTP or HTTPS with CONNECT).
	//
	// By default, Transport caches connections for future re-use.
	// This may leave many open connections when accessing many hosts.
	// This behavior can be managed using Transport's CloseIdleConnections method
	// and the MaxIdleConnsPerHost and DisableKeepAlives fields.
	//
	// Transports should be reused instead of created as needed.
	// Transports are safe for concurrent use by multiple goroutines.
	//
	// A Transport is a low-level primitive for making HTTP and HTTPS requests.
	// For high-level functionality, such as cookies and redirects, see Client.
	//
	// Transport uses HTTP/1.1 for HTTP URLs and either HTTP/1.1
	// for HTTPS URLs.
	Transport struct {
		idleMu     sync.Mutex
		wantIdle   bool                                // user has requested to close all idle conns
		idleConn   map[connectMethodKey][]*persistConn // most recently used at end
		idleConnCh map[connectMethodKey]chan *persistConn
		idleLRU    connLRU

		reqMu       sync.Mutex
		reqCanceler map[*Request]func(error)

		altMu    sync.Mutex   // guards changing altProto only
		altProto atomic.Value // of nil or map[string]RoundTripper, key is URI scheme

		// Proxy specifies a function to return a proxy for a given
		// Request. If the function returns a non-nil error, the
		// request is aborted with the provided error.
		//
		// The proxy type is determined by the URL scheme. "http"
		// and "socks5" are supported. If the scheme is empty,
		// "http" is assumed.
		//
		// If Proxy is nil or returns a nil *URL, no proxy is used.
		Proxy func(*Request) (*url.URL, error)

		// DialContext specifies the dial function for creating unencrypted TCP connections.
		// If DialContext is nil (and the deprecated Dial below is also nil),
		// then the transport dials using package net.
		DialContext func(ctx context.Context, network, addr string) (net.Conn, error)

		// DialTLS specifies an optional dial function for creating
		// TLS connections for non-proxied HTTPS requests.
		//
		// If DialTLS is nil, Dial and TLSClientConfig are used.
		//
		// If DialTLS is set, the Dial hook is not used for HTTPS
		// requests and the TLSClientConfig and TLSHandshakeTimeout
		// are ignored. The returned net.Conn is assumed to already be
		// past the TLS handshake.
		DialTLS func(network, addr string) (net.Conn, error)

		// TLSClientConfig specifies the TLS configuration to use with
		// tls.Client.
		// If nil, the default configuration is used.
		// If non-nil, HTTP/2 support may not be enabled by default.
		TLSClientConfig *tls.Config

		// TLSHandshakeTimeout specifies the maximum amount of time waiting to
		// wait for a TLS handshake. Zero means no timeout.
		TLSHandshakeTimeout time.Duration

		// DisableKeepAlives, if true, prevents re-use of TCP connections
		// between different HTTP requests.
		DisableKeepAlives bool

		// DisableCompression, if true, prevents the Transport from
		// requesting compression with an "Accept-Encoding: gzip"
		// request header when the Request contains no existing
		// Accept-Encoding value. If the Transport requests gzip on
		// its own and gets a gzipped response, it's transparently
		// decoded in the Response.Body. However, if the user
		// explicitly requested gzip it is not automatically
		// uncompressed.
		DisableCompression bool

		// MaxIdleConns controls the maximum number of idle (keep-alive)
		// connections across all hosts. Zero means no limit.
		MaxIdleConns int

		// MaxIdleConnsPerHost, if non-zero, controls the maximum idle
		// (keep-alive) connections to keep per-host. If zero,
		// DefaultMaxIdleConnsPerHost is used.
		MaxIdleConnsPerHost int

		// IdleConnTimeout is the maximum amount of time an idle
		// (keep-alive) connection will remain idle before closing
		// itself.
		// Zero means no limit.
		IdleConnTimeout time.Duration

		// ResponseHeaderTimeout, if non-zero, specifies the amount of
		// time to wait for a server's response headers after fully
		// writing the request (including its body, if any). This
		// time does not include the time to read the response body.
		ResponseHeaderTimeout time.Duration

		// ExpectContinueTimeout, if non-zero, specifies the amount of
		// time to wait for a server's first response headers after fully
		// writing the request headers if the request has an
		// "Expect: 100-continue" header. Zero means no timeout and
		// causes the body to be sent immediately, without
		// waiting for the server to approve.
		// This time does not include the time to send the request header.
		ExpectContinueTimeout time.Duration

		// TLSNextProto specifies how the Transport switches to an
		// alternate protocol (such as HTTP/2) after a TLS NPN/ALPN
		// protocol negotiation. If Transport dials an TLS connection
		// with a non-empty protocol name and TLSNextProto contains a
		// map entry for that key (such as "h2"), then the func is
		// called with the request's authority (such as "example.com"
		// or "example.com:1234") and the TLS connection. The function
		// must return a RoundTripper that then handles the request.
		// If TLSNextProto is not nil, HTTP/2 support is not enabled
		// automatically.
		// @comment : HTTP/2 is disabled - we don't need TLSNextProto
		//TLSNextProto map[string]func(authority string, c *tls.Conn) RoundTripper

		// ProxyConnectHeader optionally specifies headers to send to
		// proxies during CONNECT requests.
		ProxyConnectHeader Header

		// MaxResponseHeaderBytes specifies a limit on how many
		// response bytes are allowed in the server's response
		// header.
		//
		// Zero means to use a default limit.
		MaxResponseHeaderBytes int64
	}

	// transportRequest is a wrapper around a *Request that adds
	// optional extra headers to write and stores any error to return
	// from roundTrip.
	transportRequest struct {
		*Request        // original request, not to be mutated
		extra    Header // extra headers to write, or nil
		//TODO : @badu - find a better way perhaps
		trace *trc.ClientTrace // optional
		mu    sync.Mutex       // guards err
		err   error            // first setError value for mapRoundTripError to consider
	}

	// envOnce looks up an environment variable (optionally by multiple
	// names) once. It mitigates expensive lookups on some platforms
	// (e.g. Windows).
	envOnce struct {
		names []string
		once  sync.Once
		val   string
	}

	// transportReadFromServerError is used by Transport.readLoop when the
	// 1 byte peek read fails and we're actually anticipating a response.
	// Usually this is just due to the inherent keep-alive shut down race,
	// where the server closed the connection at the same time the client
	// wrote. The underlying err field is usually io.EOF or some
	// ECONNRESET sort of thing which varies by platform. But it might be
	// the user's custom net.Conn.Read error too, so we carry it along for
	// them to return from Transport.RoundTrip.
	transportReadFromServerError struct {
		err error
	}

	oneConnDialer <-chan net.Conn

	// persistConnWriter is the io.Writer written to by pc.bw.
	// It accumulates the number of bytes written to the underlying conn,
	// so the retry logic can determine whether any bytes made it across
	// the wire.
	// This is exactly 1 pointer field wide so it can go into an interface
	// without allocation.
	persistConnWriter struct {
		pc *persistConn
	}

	// connectMethod is the map key (in its String form) for keeping persistent
	// TCP connections alive for subsequent HTTP requests.
	//
	// A connect method may be of the following types:
	//
	// Cache key form                    Description
	// -----------------                 -------------------------
	// |http|foo.com                     http directly to server, no proxy
	// |https|foo.com                    https directly to server, no proxy
	// http://proxy.com|https|foo.com    http to proxy, then CONNECT to foo.com
	// http://proxy.com|http             http to proxy, http to anywhere after that
	// socks5://proxy.com|http|foo.com   socks5 to proxy, then http to foo.com
	// socks5://proxy.com|https|foo.com  socks5 to proxy, then https to foo.com
	//
	// Note: no support to https to the proxy yet.
	//
	connectMethod struct {
		proxyURL     *url.URL // nil for no proxy, else full proxy URL
		targetScheme string   // "http" or "https"
		targetAddr   string   // Not used if http proxy + http targetScheme (4th example in table)
	}

	// connectMethodKey is the map key version of connectMethod, with a
	// stringified proxy URL (or the empty string) instead of a pointer to
	// a URL.
	connectMethodKey struct {
		proxy, scheme, addr string
	}

	// persistConn wraps a connection, usually a persistent one
	// (but may be used for non-keep-alive requests as well)
	persistConn struct {
		// alt optionally specifies the TLS NextProto RoundTripper.
		// This is used for HTTP/2 today and future protocols later.
		// If it's non-nil, the rest of the fields are unused.
		// @comment : HTTP/2 is disabled - we don't need alt-ernate round tripper
		//alt RoundTripper

		t         *Transport
		cacheKey  connectMethodKey
		conn      net.Conn
		tlsState  *tls.ConnectionState
		br        *bufio.Reader       // from conn
		bw        *bufio.Writer       // to conn
		nwrite    int64               // bytes written
		reqch     chan requestAndChan // written by roundTrip; read by readLoop
		writech   chan writeRequest   // written by roundTrip; read by writeLoop
		closech   chan struct{}       // closed when conn closed
		isProxy   bool
		sawEOF    bool  // whether we've seen EOF from conn; owned by readLoop
		readLimit int64 // bytes allowed to be read; owned by readLoop
		// writeErrCh passes the request write error (usually nil)
		// from the writeLoop goroutine to the readLoop which passes
		// it off to the res.Body reader, which then uses it to decide
		// whether or not a connection can be reused. Issue 7569.
		writeErrCh chan error

		writeLoopDone chan struct{} // closed when write loop ends

		// Both guarded by Transport.idleMu:
		idleAt    time.Time   // time it last become idle
		idleTimer *time.Timer // holding an AfterFunc to close it

		mu                   sync.Mutex // guards following fields
		numExpectedResponses int
		closed               error // set non-nil when conn is closed, before closech is closed
		canceledErr          error // set non-nil if conn is canceled
		broken               bool  // an error has happened on this connection; marked broken so it's not reused.
		reused               bool  // whether conn has had successful request/response and is being reused.
		// mutateHeaderFunc is an optional func to modify extra
		// headers on each outbound request before it's written. (the
		// original Request given to RoundTrip is not modified)
		mutateHeaderFunc func(Header)
	}

	// nothingWrittenError wraps a write errors which ended up writing zero bytes.
	nothingWrittenError struct {
		error
	}

	// responseAndError is how the goroutine reading from an HTTP/1 server
	// communicates with the goroutine doing the RoundTrip.
	responseAndError struct {
		res *Response // else use this response (see res method)
		err error
	}

	requestAndChan struct {
		req *Request
		ch  chan responseAndError // unbuffered; always send in select on callerGone

		// whether the Transport (as opposed to the user client code)
		// added the Accept-Encoding gzip header. If the Transport
		// set it, only then do we transparently decode the gzip.
		addedGzip bool

		// Optional blocking chan for Expect: 100-continue (for send).
		// If the request has an "Expect: 100-continue" header and
		// the server responds 100 Continue, readLoop send a value
		// to writeLoop via this chan.
		continueCh chan<- struct{}

		callerGone <-chan struct{} // closed when roundTrip caller has returned
	}

	// A writeRequest is sent by the readLoop's goroutine to the
	// writeLoop's goroutine to write a request while the read loop
	// concurrently waits on both the write response and the server's
	// reply.
	writeRequest struct {
		req *transportRequest
		ch  chan<- error

		// Optional blocking chan for Expect: 100-continue (for receive).
		// If not nil, writeLoop blocks sending request body until
		// it receives from this chan.
		continueCh <-chan struct{}
	}

	// TODO : @badu -
	httpError struct {
		err     string
		timeout bool
	}
	// tLogKey is a context WithValue key for test debugging contexts containing
	// a t.Logf func. See export_test.go's Request.WithT method.
	tLogKey struct{}
	// bodyEOFSignal is used by the HTTP/1 transport when reading response
	// bodies to make sure we see the end of a response body before
	// proceeding and reading on the connection again.
	//
	// It wraps a ReadCloser but runs fn (if non-nil) at most
	// once, right before its final (error-producing) Read or Close call
	// returns. fn should return the new error to return from Read or Close.
	//
	// If earlyCloseFn is non-nil and Close is called before io.EOF is
	// seen, earlyCloseFn is called instead of fn, and its return value is
	// the return value from Close.
	bodyEOFSignal struct {
		body         io.ReadCloser
		mu           sync.Mutex        // guards following 4 fields
		closed       bool              // whether Close has been called
		rerr         error             // sticky Read error
		fn           func(error) error // err will be nil on Read io.EOF
		earlyCloseFn func() error      // optional alt Close func used if io.EOF not seen
	}

	// gzipReader wraps a response body so it can lazily
	// call gzip.NewReader on the first call to Read
	gzipReader struct {
		body *bodyEOFSignal // underlying HTTP/1 response body framing
		zr   *gzip.Reader   // lazily-initialized gzip reader
		zerr error          // any error from gzip.NewReader; sticky
	}

	tlsHandshakeTimeoutError struct{}

	connLRU struct {
		ll *list.List // list.Element.Value type is of *persistConn
		m  map[*persistConn]*list.Element
	}
)
