/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package http

import (
	"sync/atomic"
)

func (b *atomicBool) isSet() bool { return atomic.LoadInt32((*int32)(b)) != 0 }

func (b *atomicBool) setTrue() { atomic.StoreInt32((*int32)(b), 1) }
