/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package http

import "io"

func (noBody) Read([]byte) (int, error) { return 0, io.EOF }

func (noBody) Close() error { return nil }

func (noBody) WriteTo(io.Writer) (int64, error) { return 0, nil }
