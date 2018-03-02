/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package cli

import (
	"errors"
	"sync"
	"time"

	"github.com/badu/http/url"

	. "github.com/badu/http"
	. "github.com/badu/http/tport"
)

// A Client is an HTTP client. Its zero value (DefaultClient) is a
// usable client that uses DefaultTransport.
//
// The Client's Transport typically has internal state (cached TCP
// connections), so Clients should be reused instead of created as
// needed. Clients are safe for concurrent use by multiple goroutines.
//
// A Client is higher-level than a RoundTripper (such as Transport)
// and additionally handles HTTP details such as cookies and
// redirects.
//
// When following redirects, the Client will forward all headers set on the
// initial Request except:
//
// • when forwarding sensitive headers like "Authorization",
// "WWW-Authenticate", and "Cookie" to untrusted targets.
// These headers will be ignored when following a redirect to a domain
// that is not a subdomain match or exact match of the initial domain.
// For example, a redirect from "foo.com" to either "foo.com" or "sub.foo.com"
// will forward the sensitive headers, but a redirect to "bar.com" will not.
//
// • when forwarding the "Cookie" header with a non-nil cookie Jar.
// Since each redirect may mutate the state of the cookie jar,
// a redirect may possibly alter a cookie set in the initial request.
// When forwarding the "Cookie" header, any mutated cookies will be omitted,
// with the expectation that the Jar will insert those mutated cookies
// with the updated values (assuming the origin matches).
// If Jar is nil, the initial cookies are forwarded without change.
//
type Client struct {
	// Transport specifies the mechanism by which individual
	// HTTP requests are made.
	// If nil, DefaultTransport is used.
	Transport RoundTripper

	// CheckRedirect specifies the policy for handling redirects.
	// If CheckRedirect is not nil, the client calls it before
	// following an HTTP redirect. The arguments req and via are
	// the upcoming request and the requests made already, oldest
	// first. If CheckRedirect returns an error, the Client's Get
	// method returns both the previous Response (with its Body
	// closed) and CheckRedirect's error (wrapped in a url.Error)
	// instead of issuing the Request req.
	// As a special case, if CheckRedirect returns ErrUseLastResponse,
	// then the most recent response is returned with its body
	// unclosed, along with a nil error.
	//
	// If CheckRedirect is nil, the Client uses its default policy,
	// which is to stop after 10 consecutive requests.
	CheckRedirect func(req *Request, via []*Request) error

	// Jar specifies the cookie jar.
	//
	// The Jar is used to insert relevant cookies into every
	// outbound Request and is updated with the cookie values
	// of every inbound Response. The Jar is consulted for every
	// redirect that the Client follows.
	//
	// If Jar is nil, cookies are only sent if they are explicitly
	// set on the Request.
	Jar CookieJar
}

// DefaultClient is the default Client and is used by Get, Head, and Post.
var DefaultClient = &Client{}

// ErrUseLastResponse can be returned by Client.CheckRedirect hooks to
// control how redirects are processed. If returned, the next request
// is not sent and the most recent response is returned with its body
// unclosed.
var ErrUseLastResponse = errors.New("github.com/badu/http/cli: use last response")

// A CookieJar manages storage and use of cookies in HTTP requests.
//
// Implementations of CookieJar must be safe for concurrent use by multiple
// goroutines.
//
//TODO : @badu - maybe get rid of the interface
type CookieJar interface {
	// SetCookies handles the receipt of the cookies in a reply for the
	// given URL.  It may or may not choose to save the cookies, depending
	// on the jar's policy and implementation.
	SetCookies(u *url.URL, cookies []*Cookie)

	// Cookies returns the cookies to send in a request for the given URL.
	// It is up to the implementation to honor the standard cookie use
	// restrictions such as in RFC 6265.
	Cookies(u *url.URL) []*Cookie
}

//===========================
// Cookies
//===========================

// PublicSuffixList provides the public suffix of a domain. For example:
//      - the public suffix of "example.com" is "com",
//      - the public suffix of "foo1.foo2.foo3.co.uk" is "co.uk", and
//      - the public suffix of "bar.pvt.k12.ma.us" is "pvt.k12.ma.us".
//
// Implementations of PublicSuffixList must be safe for concurrent use by
// multiple goroutines.
//
// An implementation that always returns "" is valid and may be useful for
// testing but it is not secure: it means that the HTTP server for foo.com can
// set a cookie for bar.com.
//
// A public suffix list implementation is in the package
// golang.org/x/net/publicsuffix.
type PublicSuffixList interface {
	// PublicSuffix returns the public suffix of domain.
	//
	// TODO: specify which of the caller and callee is responsible for IP
	// addresses, for leading and trailing dots, for case sensitivity, and
	// for IDN/Punycode.
	PublicSuffix(domain string) string

	// String returns a description of the source of this public suffix
	// list. The description will typically contain something like a time
	// stamp or version number.
	String() string
}

// Options are the options for creating a new Jar.
type Options struct {
	// PublicSuffixList is the public suffix list that determines whether
	// an HTTP server can set a cookie for a domain.
	//
	// A nil value is valid and may be useful for testing but it is not
	// secure: it means that the HTTP server for foo.co.uk can set a cookie
	// for bar.co.uk.
	PublicSuffixList PublicSuffixList
}

// Jar implements the client.CookieJar interface from the net/ehttp/client package.
type Jar struct {
	psList PublicSuffixList

	// mu locks the remaining fields.
	mu sync.Mutex

	// entries is a set of entries, keyed by their eTLD+1 and subkeyed by
	// their name/domain/path.
	entries map[string]map[string]cookieEntry

	// nextSeqNum is the next sequence number assigned to a new cookie
	// created SetCookies.
	nextSeqNum uint64
}

// cookie_entry is the internal representation of a cookie.
//
// This struct type is not used outside of this package per se, but the exported
// fields are those of RFC 6265.
type cookieEntry struct {
	Name       string
	Value      string
	Domain     string
	Path       string
	Secure     bool
	HttpOnly   bool
	Persistent bool
	HostOnly   bool
	Expires    time.Time
	Creation   time.Time
	LastAccess time.Time

	// seqNum is a sequence number so that Cookies returns cookies in a
	// deterministic order, even for cookies that have equal Path length and
	// equal Creation time. This simplifies testing.
	seqNum uint64
}

var (
	errIllegalDomain   = errors.New("cookiejar: illegal cookie domain attribute")
	errMalformedDomain = errors.New("cookiejar: malformed cookie domain attribute")
	errNoHostname      = errors.New("cookiejar: no host name available (IP only)")
)

// endOfTime is the time when session (non-persistent) cookies expire.
// This instant is representable in most date/time formats (not just
// Go's time.Time) and should be far enough in the future.
var endOfTime = time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC)

// These parameter values are specified in section 5.
//
// All computation is done with int32s, so that overflow behavior is identical
// regardless of whether int is 32-bit or 64-bit.
const (
	base        int32 = 36
	damp        int32 = 700
	initialBias int32 = 72
	initialN    int32 = 128
	skew        int32 = 38
	tmax        int32 = 26
	tmin        int32 = 1
)

// acePrefix is the ASCII Compatible Encoding prefix.
const acePrefix = "xn--"
