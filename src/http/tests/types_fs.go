/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package tests

import (
	"io"
	"time"

	"http/filetransport"
)

const (
	testFile    = "testdata/file"
	testFileLen = 11
)

type (
	wantRange struct {
		start, end int64 // range [start,end)
	}

	testFileSystem struct {
		open func(name string) (filetransport.File, error)
	}

	fakeFileInfo struct {
		dir      bool
		basename string
		modtime  time.Time
		ents     []*fakeFileInfo
		contents string
		err      error
	}

	fakeFile struct {
		io.ReadSeeker
		fi     *fakeFileInfo
		path   string // as opened
		entpos int
	}

	fakeFS map[string]*fakeFileInfo

	issue12991FS struct{}

	issue12991File struct{ filetransport.File }

	fileServerCleanPathDir struct {
		log *[]string
	}

	panicOnSeek struct{ io.ReadSeeker }
)
