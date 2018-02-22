/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package http

import (
	"bufio"
	"io"
	"strings"
	"sync"
	"time"
)

const (
	toLower = 'a' - 'A'
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
	crnl = []byte{'\r', '\n'}

	dotcrnl = []byte{'.', '\r', '\n'}

	// commonHeader interns common header strings.
	commonHeader = make(map[string]string)

	// isTokenTable is a copy of net/http/lex.go's isTokenTable.
	// See https://httpwg.github.io/specs/rfc7230.html#rule.token.separators
	isTokenTable = [127]bool{
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
		'0':  true,
		'1':  true,
		'2':  true,
		'3':  true,
		'4':  true,
		'5':  true,
		'6':  true,
		'7':  true,
		'8':  true,
		'9':  true,
		'A':  true,
		'B':  true,
		'C':  true,
		'D':  true,
		'E':  true,
		'F':  true,
		'G':  true,
		'H':  true,
		'I':  true,
		'J':  true,
		'K':  true,
		'L':  true,
		'M':  true,
		'N':  true,
		'O':  true,
		'P':  true,
		'Q':  true,
		'R':  true,
		'S':  true,
		'T':  true,
		'U':  true,
		'W':  true,
		'V':  true,
		'X':  true,
		'Y':  true,
		'Z':  true,
		'^':  true,
		'_':  true,
		'`':  true,
		'a':  true,
		'b':  true,
		'c':  true,
		'd':  true,
		'e':  true,
		'f':  true,
		'g':  true,
		'h':  true,
		'i':  true,
		'j':  true,
		'k':  true,
		'l':  true,
		'm':  true,
		'n':  true,
		'o':  true,
		'p':  true,
		'q':  true,
		'r':  true,
		's':  true,
		't':  true,
		'u':  true,
		'v':  true,
		'w':  true,
		'x':  true,
		'y':  true,
		'z':  true,
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
	Header        map[string][]string
	writeStringer interface {
		WriteString(string) (int, error)
	}

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
