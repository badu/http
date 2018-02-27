/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package http

func (s *headerSorter) Len() int { return len(s.kvs) }

func (s *headerSorter) Swap(i, j int) { s.kvs[i], s.kvs[j] = s.kvs[j], s.kvs[i] }

func (s *headerSorter) Less(i, j int) bool { return s.kvs[i].key < s.kvs[j].key }
