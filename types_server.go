/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package http

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"log"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/badu/http/hdr"
)

const (
	// This should be >= 512 bytes for DetectContentType,
	// but otherwise it's somewhat arbitrary.
	bufferBeforeChunkingSize = 2048

	// debugServerConnections controls whether all server connections are wrapped
	// with a verbose logging wrapper.
	debugServerConnections = false

	// DefaultMaxHeaderBytes is the maximum permitted size of the headers
	// in an HTTP request.
	// This can be overridden by setting Server.MaxHeaderBytes.
	DefaultMaxHeaderBytes = 1 << 20 // 1 MB

	// TimeFormat is the time format to use when generating times in HTTP
	// headers. It is like time.RFC1123 but hard-codes GMT as the time
	// zone. The time being formatted must be in UTC for Format to
	// generate the correct format.
	//
	// For parsing this time format, see ParseTime.
	TimeFormat = "Mon, 02 Jan 2006 15:04:05 GMT"

	// maxPostHandlerReadBytes is the max number of Request.Body bytes not
	// consumed by a handler that the server will read from the client
	// in order to keep a connection alive. If there are more bytes than
	// this then the server to be paranoid instead sends a "Connection:
	// close" response.
	//
	// This number is approximately what a typical machine's TCP buffer
	// size is anyway.  (if we have the bytes on the machine, we might as
	// well read them)
	maxPostHandlerReadBytes = 256 << 10

	// rstAvoidanceDelay is the amount of time we sleep after closing the
	// write side of a TCP connection before closing the entire socket.
	// By sleeping, we increase the chances that the client sees our FIN
	// and processes its final data before they process the subsequent RST
	// from closing a connection with known unread data.
	// This RST seems to occur mostly on BSD systems. (And Windows?)
	// This timeout is somewhat arbitrary (~latency around the planet).
	rstAvoidanceDelay = 500 * time.Millisecond
)

const (
	// StateNew represents a new connection that is expected to
	// send a request immediately. Connections begin at this
	// state and then transition to either StateActive or
	// StateClosed.
	StateNew ConnState = iota

	// StateActive represents a connection that has read 1 or more
	// bytes of a request. The Server.ConnState hook for
	// StateActive fires before the request has entered a handler
	// and doesn't fire again until the request has been
	// handled. After the request is handled, the state
	// transitions to StateClosed, StateHijacked, or StateIdle.
	// For HTTP/2, StateActive fires on the transition from zero
	// to one active request, and only transitions away once all
	// active requests are complete. That means that ConnState
	// cannot be used to do per-request work; ConnState only notes
	// the overall state of the connection.
	StateActive

	// StateIdle represents a connection that has finished
	// handling a request and is in the keep-alive state, waiting
	// for a new request. Connections transition from StateIdle
	// to either StateActive or StateClosed.
	StateIdle

	// StateHijacked represents a hijacked connection.
	// This is a terminal state. It does not transition to StateClosed.
	StateHijacked

	// StateClosed represents a closed connection.
	// This is a terminal state. Hijacked connections do not
	// transition to StateClosed.
	StateClosed
)

var (
	// Errors used by the HTTP server.

	// ErrBodyNotAllowed is returned by ResponseWriter.Write calls
	// when the HTTP method or response code does not permit a
	// body.
	ErrBodyNotAllowed = errors.New("http: request method or response status code does not allow body")

	// ErrHijacked is returned by ResponseWriter.Write calls when
	// the underlying connection has been hijacked using the
	// Hijacker interface. A zero-byte write on a hijacked
	// connection will return ErrHijacked without any other side
	// effects.
	ErrHijacked = errors.New("http: connection has been hijacked")

	// ErrContentLength is returned by ResponseWriter.Write calls
	// when a Handler set a Content-Length response header with a
	// declared size and then attempted to write more bytes than
	// declared.
	ErrContentLength = errors.New("http: wrote more than the declared Content-Length")

	// SrvCtxtKey is a context key. It can be used in HTTP
	// handlers with context.WithValue to access the server that
	// started the handler. The associated value will be of
	// type *Server.
	SrvCtxtKey = &contextKey{"http-server"}

	// LocalAddrContextKey is a context key. It can be used in
	// HTTP handlers with context.WithValue to access the address
	// the local address the connection arrived on.
	// The associated value will be of type net.Addr.
	LocalAddrContextKey = &contextKey{"local-addr"}

	colonSpace = []byte(": ")

	bufioReaderPool   sync.Pool
	bufioWriter2kPool sync.Pool
	bufioWriter4kPool sync.Pool

	copyBufPool = sync.Pool{
		New: func() interface{} {
			b := make([]byte, 32*1024)
			return &b
		},
	}

	errTooLarge = errors.New("http: request too large")

	// Sorted the same as extraHeader.Write's loop.
	extraHeaderKeys = [][]byte{
		[]byte(hdr.ContentType),
		[]byte(hdr.Connection),
		[]byte(hdr.TransferEncoding),
	}

	headerContentLength = []byte("Content-Length: ")
	headerDate          = []byte("Date: ")

	_ closeWriter = (*net.TCPConn)(nil)

	// connStateInterface is an array of the interface{} versions of
	// ConnState values, so we can use them in atomic.Values later without
	// paying the cost of shoving their integers in an interface{}.
	connStateInterface = [...]interface{}{
		StateNew:      StateNew,
		StateActive:   StateActive,
		StateIdle:     StateIdle,
		StateHijacked: StateHijacked,
		StateClosed:   StateClosed,
	}
	//TODO : @badu ErrAbortHandler is something being used only in tests...
	// ErrAbortHandler is a sentinel panic value to abort a handler.
	// While any panic from ServeHTTP aborts the response to the client,
	// panicking with ErrAbortHandler also suppresses logging of a stack
	// trace to the server's error log.
	ErrAbortHandler = errors.New("github.com/badu//http: abort Handler")

	htmlReplacer = strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		// "&#34;" is shorter than "&quot;".
		`"`, "&#34;",
		// "&#39;" is shorter than "&apos;" and apos was not in HTML until HTML5.
		"'", "&#39;",
	)

	// shutdownPollInterval is how often we poll for quiescence
	// during Server.Shutdown. This is lower during tests, to
	// speed up tests.
	// Ideally we could find a solution that doesn't involve polling,
	// but which also doesn't have a high runtime cost (and doesn't
	// involve any contentious mutexes), but that is left as an
	// exercise for the reader.
	shutdownPollInterval = 500 * time.Millisecond

	stateName = map[ConnState]string{
		StateNew:      "new",
		StateActive:   "active",
		StateIdle:     "idle",
		StateHijacked: "hijacked",
		StateClosed:   "closed",
	}

	// ErrServerClosed is returned by the Server's Serve, ServeTLS, ListenAndServe,
	// and ListenAndServeTLS methods after a call to Shutdown or Close.
	ErrServerClosed = errors.New("http: Server closed")

	// ErrHandlerTimeout is returned on ResponseWriter Write calls
	// in handlers which have timed out.
	ErrHandlerTimeout = errors.New("http: Handler timeout")

	uniqNameMu   sync.Mutex
	uniqNameNext = make(map[string]int)

	//TODO : @badu - both exported for tests
	//@comment : this technique is called Method expression
	/**
	type T struct {}
	func (T) Foo(s string) { println(s) }

	var fn func(T, string) = T.Foo
	*/
	ExportServerNewConn     = (*Server).newConn
	ExportCloseWriteAndWait = (*conn).closeWriteAndWait
)

type (
	// A Handler responds to an HTTP request.
	//
	// ServeHTTP should write reply headers and data to the ResponseWriter
	// and then return. Returning signals that the request is finished; it
	// is not valid to use the ResponseWriter or read from the
	// Request.Body after or concurrently with the completion of the
	// ServeHTTP call.
	//
	// Depending on the HTTP client software, HTTP protocol version, and
	// any intermediaries between the client and the Go server, it may not
	// be possible to read from the Request.Body after writing to the
	// ResponseWriter. Cautious handlers should read the Request.Body
	// first, and then reply.
	//
	// Except for reading the body, handlers should not modify the
	// provided Request.
	//
	// If ServeHTTP panics, the server (the caller of ServeHTTP) assumes
	// that the effect of the panic was isolated to the active request.
	// It recovers the panic, logs a stack trace to the server error log,
	// and closes the network connection. To abort a handler so
	// the client sees an interrupted response but the server doesn't log
	// an error, panic with the value ErrAbortHandler.
	Handler interface {
		ServeHTTP(ResponseWriter, *Request)
	}

	// A ResponseWriter interface is used by an HTTP handler to
	// construct an HTTP response.
	//
	// A ResponseWriter may not be used after the Handler.ServeHTTP method
	// has returned.
	ResponseWriter interface {
		// Header returns the header map that will be sent by
		// WriteHeader. The Header map also is the mechanism with which
		// Handlers can set HTTP trailers.
		//
		// Changing the header map after a call to WriteHeader (or
		// Write) has no effect unless the modified headers are
		// trailers.
		//
		// There are two ways to set Trailers. The preferred way is to
		// predeclare in the headers which trailers you will later
		// send by setting the "Trailer" header to the names of the
		// trailer keys which will come later. In this case, those
		// keys of the Header map are treated as if they were
		// trailers. See the example. The second way, for trailer
		// keys not known to the Handler until after the first Write,
		// is to prefix the Header map keys with the TrailerPrefix
		// constant value. See TrailerPrefix.
		//
		// To suppress implicit response headers (such as "Date"), set
		// their value to nil.
		Header() hdr.Header

		// Write writes the data to the connection as part of an HTTP reply.
		//
		// If WriteHeader has not yet been called, Write calls
		// WriteHeader(http.StatusOK) before writing the data. If the Header
		// does not contain a Content-Type line, Write adds a Content-Type set
		// to the result of passing the initial 512 bytes of written data to
		// DetectContentType.
		//
		// Depending on the HTTP protocol version and the client, calling
		// Write or WriteHeader may prevent future reads on the
		// Request.Body. For HTTP/1.x requests, handlers should read any
		// needed request body data before writing the response. Once the
		// headers have been flushed (due to either an explicit Flusher.Flush
		// call or writing enough data to trigger a flush), the request body
		// may be unavailable.
		Write([]byte) (int, error)

		// WriteHeader sends an HTTP response header with status code.
		// If WriteHeader is not called explicitly, the first call to Write
		// will trigger an implicit WriteHeader(http.StatusOK).
		// Thus explicit calls to WriteHeader are mainly used to
		// send error codes.
		WriteHeader(int)
	}

	// The Flusher interface is implemented by ResponseWriters that allow
	// an HTTP handler to flush buffered data to the client.
	//
	// The default HTTP/1.x ResponseWriter implementations
	// support Flusher, but ResponseWriter wrappers may not. Handlers
	// should always test for this ability at runtime.
	//
	// Note that even for ResponseWriters that support Flush,
	// if the client is connected through an HTTP proxy,
	// the buffered data may not reach the client until the response
	// completes.
	Flusher interface {
		// Flush sends any buffered data to the client.
		Flush()
	}

	// The Hijacker interface is implemented by ResponseWriters that allow
	// an HTTP handler to take over the connection.
	//
	// The default ResponseWriter for HTTP/1.x connections supports
	// Hijacker, but HTTP/2 connections intentionally do not.
	// ResponseWriter wrappers may also not support Hijacker. Handlers
	// should always test for this ability at runtime.
	Hijacker interface {
		// Hijack lets the caller take over the connection.
		// After a call to Hijack the HTTP server library
		// will not do anything else with the connection.
		//
		// It becomes the caller's responsibility to manage
		// and close the connection.
		//
		// The returned net.Conn may have read or write deadlines
		// already set, depending on the configuration of the
		// Server. It is the caller's responsibility to set
		// or clear those deadlines as needed.
		//
		// The returned bufio.Reader may contain unprocessed buffered
		// data from the client.
		//
		// After a call to Hijack, the original Request.Body should
		// not be used.
		Hijack() (net.Conn, *bufio.ReadWriter, error)
	}

	// The CloseNotifier interface is implemented by ResponseWriters which
	// allow detecting when the underlying connection has gone away.
	//
	// This mechanism can be used to cancel long operations on the server
	// if the client has disconnected before the response is ready.
	CloseNotifier interface {
		// CloseNotify returns a channel that receives at most a
		// single value (true) when the client connection has gone
		// away.
		//
		// CloseNotify may wait to notify until Request.Body has been
		// fully read.
		//
		// After the Handler has returned, there is no guarantee
		// that the channel receives a value.
		//
		// If the protocol is HTTP/1.1 and CloseNotify is called while
		// processing an idempotent request (such a GET) while
		// HTTP/1.1 pipelining is in use, the arrival of a subsequent
		// pipelined request may cause a value to be sent on the
		// returned channel. In practice HTTP/1.1 pipelining is not
		// enabled in browsers and not seen often in the wild.
		CloseNotify() <-chan bool
	}

	// A conn represents the server side of an HTTP connection.
	conn struct {
		// cancelCtx cancels the connection-level context.
		cancelCtx context.CancelFunc

		// netConIface is the underlying network connection.
		// This is never wrapped by other types and is the value given out
		// to CloseNotifier callers. It is usually of type *net.TCPConn or *tls.Conn.
		netConIface net.Conn

		// tlsState is the TLS connection state when using TLS.
		// nil means not TLS.
		tlsState *tls.ConnectionState

		// wErr is set to the first write error to netConIface.
		// It is set via checkConnErrorWriter{w}, where bufWriter writes.
		wErr error

		// r is bufReader's read source. It's a wrapper around netConIface that provides
		// io.LimitedReader-style limiting (while reading request headers)
		// and functionality to support CloseNotifier. See *connReader docs.
		reader *connReader

		// bufReader reads from r.
		bufReader *bufio.Reader

		// bufWriter writes to checkConnErrorWriter{c}, which populates wErr on error.
		bufWriter *bufio.Writer

		// lastMethod is the method of the most recent request
		// on this connection, if any.
		lastMethod string

		curReq   atomic.Value // of *response (which has a Request in it)
		curState atomic.Value // of ConnState

		// mu guards wasHijacked
		mu sync.Mutex

		// wasHijacked is whether this connection has been hijacked
		// by a Handler with the Hijacker interface.
		// It is guarded by mu.
		wasHijacked bool
	}

	// chunkWriter writes to a response's conn buffer, and is the writer
	// wrapped by the response.bufWriter buffered writer.
	//
	// chunkWriter also is responsible for finalizing the Header, including
	// conditionally setting the Content-Type and setting a Content-Length
	// in cases where the handler's final output is smaller than the buffer
	// size. It also conditionally adds chunk headers, when in chunking mode.
	//
	// See the comment above (*response).Write for the entire write flow.
	chunkWriter struct {
		res *response

		// header is either nil or a deep clone of res.handlerHeader
		// at the time of res.WriteHeader, if res.WriteHeader is
		// called and extra buffering is being done to calculate
		// Content-Type and/or Content-Length.
		header hdr.Header

		// wroteHeader tells whether the header's been written to "the
		// wire" (or rather: w.conn.buf). this is unlike
		// (*response).wroteHeader, which tells only whether it was
		// logically written.
		wroteHeader bool

		// set by the writeHeader method:
		chunking bool // using chunked transfer encoding for reply body
	}

	// A response represents the server side of an HTTP response.
	response struct {
		conn      *conn
		ctx       context.Context
		req       *Request // request for this response
		reqBody   io.ReadCloser
		cancelCtx context.CancelFunc // when ServeHTTP exits
		bufWriter *bufio.Writer      // buffers output in chunks to chunkWriter
		// TODO : @badu - maybe this is chunked_writer after all ?
		chunkWriter chunkWriter

		// handlerHeader is the Header that Handlers get access to,
		// which may be retained and mutated even after WriteHeader.
		// handlerHeader is copied into cw.header at WriteHeader
		// time, and privately mutated thereafter.
		handlerHeader hdr.Header

		written       int64 // number of bytes written in body
		contentLength int64 // explicitly-declared Content-Length; or -1
		status        int   // status code passed to WriteHeader

		//TODO : @badu - too much booleans (state / bitflag?)
		wroteHeader         bool // reply header has been (logically) written
		wroteContinue       bool // 100 Continue response was written
		wants10KeepAlive    bool // HTTP/1.0 w/ Connection "keep-alive"
		wantsClose          bool // HTTP request has Connection "close"
		calledHeader        bool // handler accessed handlerHeader via Header
		closeAfterReply     bool // close connection after this reply.  set on request and updated after response from handler if there's a "Connection: keep-alive" response header and a Content-Length.
		requestBodyLimitHit bool // requestBodyLimitHit is set by requestTooLarge when maxBytesReader hits its max size. It is checked in WriteHeader, to make sure we don't consume the remaining request body to try to advance to the next HTTP request. Instead, when this is set, we stop reading subsequent requests on this connection and stop reading input from it.
		// closeNotifyCh is the channel returned by CloseNotify.
		// TODO(bradfitz): this is currently (for Go 1.8) always non-nil. Make this lazily-created again as it used to be?
		closeNotifyCh chan bool
		// trailers are the headers to be sent after the handler finishes writing the body. This field is initialized from
		// the Trailer response header when the response header is written.
		trailers []string

		handlerDone atomicBool // set true when the handler exits

		// Buffers for Date, Content-Length, and status code
		dateBuf   [len(TimeFormat)]byte
		clenBuf   [10]byte
		statusBuf [3]byte

		didCloseNotify int32 // atomic (only 0->1 winner should send)
	}

	atomicBool int32

	// writerOnly hides an io.Writer value's optional ReadFrom method
	// from io.Copy.
	writerOnly struct {
		io.Writer
	}

	// connReader is the io.Reader wrapper used by *conn. It combines a
	// selectively-activated io.LimitedReader (to bound request header
	// read sizes) with support for selectively keeping an io.Reader.Read
	// call blocked in a background goroutine to wait for activity and
	// trigger a CloseNotifier channel.
	connReader struct {
		mu      sync.Mutex // guards following
		conn    *conn
		byteBuf [1]byte
		cond    *sync.Cond
		hasByte bool
		inRead  bool
		aborted bool  // set true before conn.netConIface deadline is set to past
		remain  int64 // bytes remaining
	}

	// wrapper around io.ReadCloser which on first read, sends an
	// HTTP/1.1 100 Continue header
	expectContinueReader struct {
		resp       *response
		readCloser io.ReadCloser
		closed     bool
		sawEOF     bool
	}

	// extraHeader is the set of headers sometimes added by chunkWriter.writeHeader.
	// This type is used to avoid extra allocations from cloning and/or populating
	// the response Header map and all its 1-element slices.
	extraHeader struct {
		contentType      string
		connection       string
		transferEncoding string
		date             []byte // written if not nil
		contentLength    []byte // written if not nil
	}

	closeWriter interface {
		CloseWrite() error
	}

	// badRequestError is a literal string (used by in the server in HTML,
	// unescaped) to tell the user why their request was bad. It should
	// be plain text without user info or other embedded errors.
	badRequestError string

	// The HandlerFunc type is an adapter to allow the use of
	// ordinary functions as HTTP handlers. If f is a function
	// with the appropriate signature, HandlerFunc(f) is a
	// Handler that calls f.
	HandlerFunc func(ResponseWriter, *Request)

	// Redirect to a fixed URL
	redirectHandler struct {
		url  string
		code int
	}

	TLSConHandler func(*tls.Conn, Handler)
	// A Server defines parameters for running an HTTP server.
	// The zero value for Server is a valid configuration.
	Server struct {
		Addr      string      // TCP address to listen on, ":http" if empty
		Handler   Handler     // handler to invoke
		TLSConfig *tls.Config // optional TLS config, used by ServeTLS and ListenAndServeTLS

		// ReadTimeout is the maximum duration for reading the entire
		// request, including the body.
		//
		// Because ReadTimeout does not let Handlers make per-request
		// decisions on each request body's acceptable deadline or
		// upload rate, most users will prefer to use
		// ReadHeaderTimeout. It is valid to use them both.
		ReadTimeout time.Duration

		// ReadHeaderTimeout is the amount of time allowed to read
		// request headers. The connection's read deadline is reset
		// after reading the headers and the Handler can decide what
		// is considered too slow for the body.
		ReadHeaderTimeout time.Duration

		// WriteTimeout is the maximum duration before timing out
		// writes of the response. It is reset whenever a new
		// request's header is read. Like ReadTimeout, it does not
		// let Handlers make decisions on a per-request basis.
		WriteTimeout time.Duration

		// IdleTimeout is the maximum amount of time to wait for the
		// next request when keep-alives are enabled. If IdleTimeout
		// is zero, the value of ReadTimeout is used. If both are
		// zero, ReadHeaderTimeout is used.
		IdleTimeout time.Duration

		// MaxHeaderBytes controls the maximum number of bytes the
		// server will read parsing the request header's keys and
		// values, including the request line. It does not limit the
		// size of the request body.
		// If zero, DefaultMaxHeaderBytes is used.
		MaxHeaderBytes int

		// TLSNextProto optionally specifies a function to take over
		// ownership of the provided TLS connection when an NPN/ALPN
		// protocol upgrade has occurred. The map key is the protocol
		// name negotiated. The Handler argument should be used to
		// handle HTTP requests and will initialize the Request's TLS
		// and RemoteAddr if not already set. The connection is
		// automatically closed when the function returns.
		// If TLSNextProto is not nil, HTTP/2 support is not enabled
		// automatically.
		TLSNextProto map[string]TLSConHandler

		// ConnState specifies an optional callback function that is
		// called when a client connection changes state. See the
		// ConnState type and associated constants for details.
		ConnState func(net.Conn, ConnState)

		// ErrorLog specifies an optional logger for errors accepting
		// connections and unexpected behavior from handlers.
		// If nil, logging goes to os.Stderr via the log package's
		// standard logger.
		ErrorLog *log.Logger

		disableKeepAlives int32 // accessed atomically.
		inShutdown        int32 // accessed atomically (non-zero means we're in Shutdown)

		mu       sync.Mutex
		listener net.Listener

		activeConn map[*conn]struct{}
		doneChan   chan struct{}
		onShutdown []func()
	}

	// A ConnState represents the state of a client connection to a server.
	// It's used by the optional Server.ConnState hook.
	ConnState int

	// serverHandler delegates to either the server's Handler or
	// DefaultServeMux and also handles "OPTIONS *" requests.
	serverHandler struct {
		srv *Server
	}

	timeoutHandler struct {
		// When set, no timer will be created and this channel will
		// be used instead.
		testTimeout <-chan time.Time
		handler     Handler
		body        string
		dt          time.Duration
	}

	timeoutWriter struct {
		respWriter  ResponseWriter
		header      hdr.Header
		wbuf        bytes.Buffer
		mu          sync.Mutex
		timedOut    bool
		wroteHeader bool
		code        int
	}

	// tcpKeepAliveListener sets TCP keep-alive timeouts on accepted
	// connections. It's used by ListenAndServe and ListenAndServeTLS so
	// dead TCP connections (e.g. closing laptop mid-download) eventually
	// go away.
	tcpKeepAliveListener struct {
		*net.TCPListener
	}

	// globalOptionsHandler responds to "OPTIONS *" requests.
	globalOptionsHandler struct{}

	// initNPNRequest is an HTTP handler that initializes certain
	// uninitialized fields in its *Request. Such partially-initialized
	// Requests come from NPN protocol handlers.
	initNPNRequest struct {
		tlsConn *tls.Conn
		handler serverHandler
	}

	// loggingConn is used for debugging.
	loggingConn struct {
		name string
		net.Conn
	}

	// checkConnErrorWriter writes to c.netConIface and records any write errors to c.wErr.
	// It only contains one field (and a pointer field at that), so it
	// fits in an interface value without an extra allocation.
	checkConnErrorWriter struct {
		con *conn
	}
)
