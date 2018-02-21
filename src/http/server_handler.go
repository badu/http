/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package http

// @comment : default server handler (implements Handler)
func (sh serverHandler) ServeHTTP(rw ResponseWriter, req *Request) {
	handler := sh.srv.Handler
	if handler == nil {
		panic("Badu : DefaultServeMux was moved / disabled. Provide a handler, don't be lazy!")
		//handler = DefaultServeMux
	}
	if req.RequestURI == "*" && req.Method == OPTIONS {
		handler = globalOptionsHandler{}
	}
	handler.ServeHTTP(rw, req)
}
