/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package http

import "errors"

func (l *maxBytesReader) Read(p []byte) (int, error) {
	if l.err != nil {
		return 0, l.err
	}
	if len(p) == 0 {
		return 0, nil
	}
	// If they asked for a 32KB byte read but only 5 bytes are
	// remaining, no need to read 32KB. 6 bytes will answer the
	// question of the whether we hit the limit or go past it.
	if int64(len(p)) > l.bytesRemaining+1 {
		p = p[:l.bytesRemaining+1]
	}
	n, err := l.readCloser.Read(p)

	if int64(n) <= l.bytesRemaining {
		l.bytesRemaining -= int64(n)
		l.err = err
		return n, err
	}

	n = int(l.bytesRemaining)
	l.bytesRemaining = 0

	if res, ok := l.respWriter.(requestTooLarger); ok {
		res.requestTooLarge()
	}
	l.err = errors.New("http: request body too large")
	return n, l.err
}

func (l *maxBytesReader) Close() error {
	return l.readCloser.Close()
}
