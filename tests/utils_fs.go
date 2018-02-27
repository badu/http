/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package tests

import (
	"io"
	"io/ioutil"
	"os"
	"path"
	"strings"
	"testing"
	"time"

	. "github.com/badu/http"
	"github.com/badu/http/cli"
	"github.com/badu/http/filetransport"
)

func (issue12991FS) Open(string) (filetransport.File, error) { return issue12991File{}, nil }

func (issue12991File) Stat() (os.FileInfo, error) { return nil, os.ErrPermission }

func (issue12991File) Close() error { return nil }

func (d fileServerCleanPathDir) Open(path string) (filetransport.File, error) {
	*(d.log) = append(*(d.log), path)
	if path == "/" || path == "/dir" || path == "/dir/" {
		// Just return back something that's a directory.
		return filetransport.Dir(".").Open(".")
	}
	return nil, os.ErrNotExist
}

func (fs *testFileSystem) Open(name string) (filetransport.File, error) {
	return fs.open(name)
}

func (fs fakeFS) Open(name string) (filetransport.File, error) {
	name = path.Clean(name)
	f, ok := fs[name]
	if !ok {
		return nil, os.ErrNotExist
	}
	if f.err != nil {
		return nil, f.err
	}
	return &fakeFile{ReadSeeker: strings.NewReader(f.contents), fi: f, path: name}, nil
}

func (f *fakeFileInfo) Name() string { return f.basename }

func (f *fakeFileInfo) Sys() interface{} { return nil }

func (f *fakeFileInfo) ModTime() time.Time { return f.modtime }

func (f *fakeFileInfo) IsDir() bool { return f.dir }

func (f *fakeFileInfo) Size() int64 { return int64(len(f.contents)) }

func (f *fakeFileInfo) Mode() os.FileMode {
	if f.dir {
		return 0755 | os.ModeDir
	}
	return 0644
}

func (f *fakeFile) Close() error { return nil }

func (f *fakeFile) Stat() (os.FileInfo, error) { return f.fi, nil }

func (f *fakeFile) Readdir(count int) ([]os.FileInfo, error) {
	if !f.fi.dir {
		return nil, os.ErrInvalid
	}
	var fis []os.FileInfo

	limit := f.entpos + count
	if count <= 0 || limit > len(f.fi.ents) {
		limit = len(f.fi.ents)
	}
	for ; f.entpos < limit; f.entpos++ {
		fis = append(fis, f.fi.ents[f.entpos])
	}

	if len(fis) == 0 && count > 0 {
		return fis, io.EOF
	} else {
		return fis, nil
	}
}

func mustRemoveAll(dir string) {
	err := os.RemoveAll(dir)
	if err != nil {
		panic(err)
	}
}

func mustStat(t *testing.T, fileName string) os.FileInfo {
	fi, err := os.Stat(fileName)
	if err != nil {
		t.Fatal(err)
	}
	return fi
}

func getBody(t *testing.T, testName string, req Request, client *cli.Client) (*Response, []byte) {
	r, err := client.Do(&req)
	if err != nil {
		t.Fatalf("%s: for URL %q, send error: %v", testName, req.URL.String(), err)
	}
	b, err := ioutil.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("%s: for URL %q, reading body: %v", testName, req.URL.String(), err)
	}
	return r, b
}

func checker(t *testing.T) func(string, error) {
	return func(call string, err error) {
		if err == nil {
			return
		}
		t.Fatalf("%s: %v", call, err)
	}
}
