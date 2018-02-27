/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package filetransport

import (
	. "github.com/badu/http"
)

func (t fileTransport) RoundTrip(req *Request) (resp *Response, err error) {
	// We start ServeHTTP in a goroutine, which may take a long
	// time if the file is large. The newPopulateResponseWriter
	// call returns a channel which either ServeHTTP or finish()
	// sends our *Response on, once the *Response itself has been
	// populated (even if the body itself is still being
	// written to the res.Body, a pipe)
	rw, resc := newPopulateResponseWriter()
	go func() {
		t.fh.ServeHTTP(rw, req)
		rw.finish()
	}()
	return <-resc, nil
}
