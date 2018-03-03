/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package filetransport

import (
	"errors"
	"io"
	"os"
	"strings"
	"time"

	. "github.com/badu/http"
)

const (
	condNone condResult = iota
	condTrue
	condFalse
)

const (
	wSlash = "W/" // ATTN : do not change - will break
)

type (
	// fileTransport implements RoundTripper for the 'file' protocol.
	fileTransport struct {
		fh fileHandler
	}

	// populateResponse is a ResponseWriter that populates the *Response
	// in res, and writes its body to a pipe connected to the response
	// body. Once writes begin or finish() is called, the response is sent
	// on ch.
	populateResponse struct {
		res          *Response
		ch           chan *Response
		wroteHeader  bool
		hasContent   bool
		sentResponse bool
		pw           *io.PipeWriter
	}

	// A Dir implements FileSystem using the native file system restricted to a
	// specific directory tree.
	//
	// While the FileSystem.Open method takes '/'-separated paths, a Dir's string
	// value is a filename on the native file system, not a URL, so it is separated
	// by filepath.Separator, which isn't necessarily '/'.
	//
	// Note that Dir will allow access to files and directories starting with a
	// period, which could expose sensitive directories like a .git directory or
	// sensitive files like .htpasswd. To exclude files with a leading period,
	// remove the files/directories from the server or create a custom FileSystem
	// implementation.
	//
	// An empty Dir is treated as ".".
	Dir string

	// A FileSystem implements access to a collection of named files.
	// The elements in a file path are separated by slash ('/', U+002F)
	// characters, regardless of host operating system convention.
	FileSystem interface {
		Open(name string) (File, error)
	}

	// A File is returned by a FileSystem's Open method and can be
	// served by the FileServer implementation.
	//
	// The methods should behave the same as those on an *os.File.
	File interface {
		io.Closer
		io.Reader
		io.Seeker
		Readdir(count int) ([]os.FileInfo, error)
		Stat() (os.FileInfo, error)
	}

	// condResult is the result of an HTTP request precondition check.
	// See https://tools.ietf.org/html/rfc7232 section 3.
	condResult int

	fileHandler struct {
		root FileSystem
	}

	// httpRange specifies the byte range to be sent to the client.
	httpRange struct {
		start, length int64
	}

	// countingWriter counts how many bytes have been written to it.
	countingWriter int64
)

var (
	// errSeeker is returned by ServeContent's sizeFunc when the content
	// doesn't seek properly. The underlying Seeker's error text isn't
	// included in the sizeFunc reply so it's not sent over HTTP to end
	// users.
	errSeeker = errors.New("seeker can't seek")

	// errNoOverlap is returned by serveContent's parseRange if first-byte-pos of
	// all of the byte-range-spec values is greater than the content size.
	errNoOverlap = errors.New("invalid range: failed to overlap")

	unixEpochTime = time.Unix(0, 0)

	htmlReplacer = strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		// "&#34;" is shorter than "&quot;".
		`"`, "&#34;",
		// "&#39;" is shorter than "&apos;" and apos was not in HTML until HTML5.
		"'", "&#39;",
	)
)
