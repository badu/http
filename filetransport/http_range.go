/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package filetransport

import (
	"fmt"

	"github.com/badu/http/hdr"
)

func (r httpRange) contentRange(size int64) string {
	return fmt.Sprintf("bytes %d-%d/%d", r.start, r.start+r.length-1, size)
}

func (r httpRange) mimeHeader(contentType string, size int64) hdr.Header {
	return hdr.Header{
		hdr.ContentRange: {r.contentRange(size)},
		hdr.ContentType:  {contentType},
	}
}
