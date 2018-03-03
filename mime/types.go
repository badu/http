/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package mime

import (
	"bufio"
	"errors"
	"io"
	"strings"

	. "github.com/badu/http/hdr"
)

type (
	// A Writer generates multipart messages.
	Writer struct {
		w        io.Writer
		boundary string
		lastpart *part
	}

	part struct {
		mw     *Writer
		closed bool
		we     error // last error that occurred writing
	}
	// Form is a parsed multipart form.
	// Its File parts are stored either in memory or on disk,
	// and are accessible via the *FileHeader's Open method.
	// Its Value parts are stored as strings.
	// Both are keyed by field name.
	Form struct {
		Value map[string][]string
		File  map[string][]*FileHeader
	}

	// A FileHeader describes a file part of a multipart request.
	FileHeader struct {
		Filename string
		Header   Header
		Size     int64

		content []byte
		tmpfile string
	}

	// File is an interface to access the file part of a multipart message.
	// Its contents may be either stored in memory or on disk.
	// If stored on disk, the File's underlying concrete type will be an *os.File.
	File interface {
		io.Reader
		io.ReaderAt
		io.Seeker
		io.Closer
	}

	sectionReadCloser struct {
		*io.SectionReader
	}

	// A Part represents a single part in a multipart body.
	Part struct {
		// The headers of the body, if any, with the keys canonicalized
		// in the same fashion that the Go http.Request headers are.
		// For example, "foo-bar" changes case to "Foo-Bar"
		//
		// As a special case, if the "Content-Transfer-Encoding" header
		// has a value of "quoted-printable", that header is instead
		// hidden from this map and the body is transparently decoded
		// during Read calls.
		Header Header

		mr *Reader

		disposition       string
		dispositionParams map[string]string

		// r is either a reader directly reading from mr, or it's a
		// wrapper around such a reader, decoding the
		// Content-Transfer-Encoding
		r io.Reader

		n       int   // known data bytes waiting in mr.bufReader
		total   int64 // total data bytes read already
		err     error // error to return when n == 0
		readErr error // read error observed from mr.bufReader
	}

	// stickyErrorReader is an io.Reader which never calls Read on its
	// underlying Reader once an error has been seen. (the io.Reader
	// interface's contract promises nothing about the return values of
	// Read calls after an error, yet this package does do multiple Reads
	// after error)
	stickyErrorReader struct {
		r   io.Reader
		err error
	}

	// partReader implements io.Reader by reading raw bytes directly from the
	// wrapped *Part, without doing any Transfer-Encoding decoding.
	partReader struct {
		p *Part
	}

	// Reader is an iterator over parts in a MIME multipart body.
	// Reader's underlying parser consumes its input as needed. Seeking
	// isn't supported.
	Reader struct {
		bufReader *bufio.Reader

		currentPart *Part
		partsRead   int

		nl               []byte // "\r\n" or "\n" (set after seeing first boundary line)
		nlDashBoundary   []byte // nl + "--boundary"
		dashBoundaryDash []byte // "--boundary--"
		dashBoundary     []byte // "--boundary"
	}
	// A WordEncoder is an RFC 2047 encoded-word encoder.
	WordEncoder byte

	// QuotedReader is a quoted-printable decoder.
	QuotedReader struct {
		br   *bufio.Reader
		rerr error  // last read error
		line []byte // to be consumed before more of br
	}
)

var (
	quoteEscaper = strings.NewReplacer("\\", "\\\\", `"`, "\\\"")

	emptyParams = make(map[string]string)
	// ErrMessageTooLarge is returned by ReadForm if the message form
	// data is too large to be processed.
	ErrMessageTooLarge = errors.New("multipart: message too large")
	// ErrInvalidMediaParameter is returned by ParseMediaType if
	// the media type value was found but there was an error parsing
	// the optional parameters
	ErrInvalidMediaParameter = errors.New("mime: invalid media parameter")

	crlf       = []byte("\r\n")
	lf         = []byte("\n")
	softSuffix = []byte("=")
)

const (
	// This constant needs to be at least 76 for this package to work correctly.
	// This is because \r\n--separator_of_len_70- would fill the buffer and it
	// wouldn't be safe to consume a single byte from it.
	peekBufferSize = 4096
	// maxContentLen is how much content can be encoded, ignoring the header and
	// 2-byte footer.
	upperhex           = "0123456789ABCDEF"
	lineMaxLen         = 76
	ContentDisposition = "Content-Disposition"
)
