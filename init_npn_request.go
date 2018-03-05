/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package http

import "crypto/tls"

func (h initNPNRequest) ServeHTTP(w ResponseWriter, r *Request) {
	if r.TLS == nil {
		r.TLS = &tls.ConnectionState{}
		*r.TLS = h.tlsConn.ConnectionState()
	}
	if r.Body == nil {
		r.Body = NoBody
	}
	if r.RemoteAddr == "" {
		r.RemoteAddr = h.tlsConn.RemoteAddr().String()
	}
	h.handler.ServeHTTP(w, r)
}
