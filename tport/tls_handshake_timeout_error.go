/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package tport

func (tlsHandshakeTimeoutError) Timeout() bool { return true }

func (tlsHandshakeTimeoutError) Temporary() bool { return true }

func (tlsHandshakeTimeoutError) Error() string {
	return "github.com/badu/http/tport: TLS handshake timeout"
}
