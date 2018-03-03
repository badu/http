/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package filetransport

import (
	"path"

	. "github.com/badu/http"
)

func (f *fileHandler) ServeHTTP(w ResponseWriter, r *Request) {
	upath := r.URL.Path
	//@comment : was `if !strings.HasPrefix(upath, "/") {`
	if len(upath) < 1 || upath[:1] != "/" {
		upath = "/" + upath
		r.URL.Path = upath
	}
	serveFile(w, r, f.root, path.Clean(upath), true)
}
