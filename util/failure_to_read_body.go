/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package util

func (failureToReadBody) Read([]byte) (int, error) { return 0, errNoBody }

func (failureToReadBody) Close() error { return nil }
