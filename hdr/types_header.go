/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package hdr

import (
	"bufio"
	"io"
	"strings"
	"sync"
	"time"
)

const (
	toLower = 'a' - 'A'

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

	TimeFormat = "Mon, 02 Jan 2006 15:04:05 GMT"
)

var (
	timeFormats = []string{
		TimeFormat,
		time.RFC850,
		time.ANSIC,
	}

	headerNewlineToSpace = strings.NewReplacer("\n", " ", "\r", " ")

	headerSorterPool = sync.Pool{
		New: func() interface{} { return new(headerSorter) },
	}

	// commonHeader interns common header strings.
	commonHeader = make(map[string]string)

	// isTokenTable is a copy of net/http/lex.go's isTokenTable.
	// See https://httpwg.github.io/specs/rfc7230.html#rule.token.separators
	isTokenTable = [127]bool{
		'0': true, '1': true, '2': true, '3': true, '4': true, '5': true, '6': true, '7': true,
		'8': true, '9': true,

		'a': true, 'b': true, 'c': true, 'd': true, 'e': true, 'f': true, 'g': true, 'h': true,
		'i': true, 'j': true, 'k': true, 'l': true, 'm': true, 'n': true, 'o': true, 'p': true,
		'q': true, 'r': true, 's': true, 't': true, 'u': true, 'v': true, 'w': true, 'x': true,
		'y': true, 'z': true,

		'A': true, 'B': true, 'C': true, 'D': true, 'E': true, 'F': true, 'G': true, 'H': true,
		'I': true, 'J': true, 'K': true, 'L': true, 'M': true, 'N': true, 'O': true, 'P': true,
		'Q': true, 'R': true, 'S': true, 'T': true, 'U': true, 'V': true, 'W': true, 'X': true,
		'Y': true, 'Z': true,

		'!':  true,
		'#':  true,
		'$':  true,
		'%':  true,
		'&':  true,
		'\'': true,
		'*':  true,
		'+':  true,
		'-':  true,
		'.':  true,
		'^':  true,
		'_':  true,
		'`':  true,
		'|':  true,
		'~':  true,
	}
)

type (
	// A Reader implements convenience methods for reading requests
	// or responses from a text protocol network connection.
	HeaderReader struct {
		R   *bufio.Reader
		dot *headerDotReader
		buf []byte // a re-usable buffer for readContinuedLineSlice
	}

	headerDotReader struct {
		r     *HeaderReader
		state int
	}

	// A Header represents the key-value pairs in an HTTP header.
	Header map[string][]string
	// @comment : in "strings" package there is the same thing called stringWriterIface
	writeStringer interface {
		WriteString(string) (int, error)
	}

	// @comment : in "strings" package there is something similar called stringWriter
	// stringWriter implements the interface above WriteString on a Writer.
	stringWriter struct {
		w io.Writer
	}

	keyValues struct {
		key    string
		values []string
	}

	// A headerSorter implements sort.Interface by sorting a []keyValues
	// by key. It's used as a pointer, so it can fit in a sort.Interface
	// interface value without allocation.
	headerSorter struct {
		kvs []keyValues
	}
)
