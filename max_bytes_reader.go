/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package http

import "errors"

func (r *maxBytesReader) Read(p []byte) (int, error) {
	if r.err != nil {
		return 0, r.err
	}
	if len(p) == 0 {
		return 0, nil
	}
	// If they asked for a 32KB byte read but only 5 bytes are
	// remaining, no need to read 32KB. 6 bytes will answer the
	// question of the whether we hit the limit or go past it.
	if int64(len(p)) > r.bytesRemaining+1 {
		p = p[:r.bytesRemaining+1]
	}
	n, err := r.readCloser.Read(p)

	if int64(n) <= r.bytesRemaining {
		r.bytesRemaining -= int64(n)
		r.err = err
		return n, err
	}

	n = int(r.bytesRemaining)
	r.bytesRemaining = 0

	if res, ok := r.respWriter.(requestTooLarger); ok {
		res.requestTooLarge()
	}
	r.err = errors.New("http: request body too large")
	return n, r.err
}

func (r *maxBytesReader) Close() error {
	return r.readCloser.Close()
}
