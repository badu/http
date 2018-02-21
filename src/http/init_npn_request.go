/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package http

import "crypto/tls"

func (h initNPNRequest) ServeHTTP(rw ResponseWriter, req *Request) {
	if req.TLS == nil {
		req.TLS = &tls.ConnectionState{}
		*req.TLS = h.c.ConnectionState()
	}
	if req.Body == nil {
		req.Body = NoBody
	}
	if req.RemoteAddr == "" {
		req.RemoteAddr = h.c.RemoteAddr().String()
	}
	h.h.ServeHTTP(rw, req)
}
