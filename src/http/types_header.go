/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package http

import (
	"io"
	"strings"
	"sync"
	"time"
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
)

type (
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
