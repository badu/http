/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package http

import (
	"bufio"
	"errors"
	"io"
	"sync"
)

var (
	suppressedHeaders304    = []string{ContentType, ContentLength, TransferEncoding}
	suppressedHeadersNoBody = []string{ContentLength, TransferEncoding}

	// ErrBodyReadAfterClose is returned when reading a Request or Response
	// Body after the body has been closed. This typically happens when the body is
	// read after an HTTP Handler calls WriteHeader or Write on its
	// ResponseWriter.
	ErrBodyReadAfterClose = errors.New("http: invalid Read on closed Body")

	singleCRLF = []byte("\r\n")
	doubleCRLF = []byte("\r\n\r\n")

	errTrailerEOF = errors.New("http: unexpected EOF reading trailer")
)

type (
	readResult struct {
		n   int
		err error
		b   byte // byte read, if n == 1
	}

	errorReader struct {
		err error
	}

	byteReader struct {
		byt  byte
		done bool
	}

	// transferBodyReader is an io.Reader that reads from transferWriter.Body
	// and records any non-EOF error in transferWriter.bodyReadError.
	// It is exactly 1 pointer wide to avoid allocations into interfaces.
	// TODO : @badu investigate "It is exactly 1 pointer wide to avoid allocations into interfaces."
	transferBodyReader struct{ transferWriter *transferWriter }

	// transferWriter inspects the fields of a user-supplied Request or Response,
	// sanitizes them without changing the user object and provides methods for
	// writing the respective header, body and trailer in wire format.
	transferWriter struct {
		Method           string
		Body             io.Reader
		BodyCloser       io.Closer
		ResponseToHEAD   bool
		ContentLength    int64 // -1 means unknown, 0 means exactly none
		Close            bool
		TransferEncoding []string
		Header           Header
		Trailer          Header
		IsResponse       bool
		bodyReadError    error           // any non-EOF error from reading Body
		FlushHeaders     bool            // flush headers to network before body
		ByteReadCh       chan readResult // non-nil if probeRequestBody called
	}

	//TODO : @badu - whay all these properties are public?
	transferReader struct {
		// Input
		Header        Header
		StatusCode    int
		RequestMethod string
		ProtoMajor    int
		ProtoMinor    int
		// Output
		Body             io.ReadCloser
		ContentLength    int64
		TransferEncoding []string
		Close            bool
		Trailer          Header
	}

	// body turns a Reader into a ReadCloser.
	// Close ensures that the body has been fully read
	// and then reads the trailer if necessary.
	body struct {
		mu                    sync.Mutex // guards following, and calls to Read and Close
		reader                io.Reader
		responseOrRequestIntf interface{}   // non-nil (Response or Request) value means read trailer
		bufReader             *bufio.Reader // underlying wire-format reader for the trailer
		isClosing             bool          // is the connection to be closed after reading body?
		doEarlyClose          bool          // whether Close should stop early
		hasSawEOF             bool
		isClosed              bool
		isEarlyClose          bool   // Close called and we didn't read to the end of src
		onHitEOF              func() // if non-nil, func to call when EOF is Read
	}

	// bodyLocked is a io.Reader reading from a *body when its mutex is
	// already held.
	bodyLocked struct {
		body *body
	}

	// finishAsyncByteRead finishes reading the 1-byte sniff
	// from the ContentLength==0, Body!=nil case.
	finishAsyncByteRead struct {
		transferWriter *transferWriter
	}
)
