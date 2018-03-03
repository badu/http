/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package mime

import (
	. "github.com/badu/http/hdr"
	"io"
	"io/ioutil"
)

// FormName returns the name parameter if p has a Content-Disposition
// of type "form-data".  Otherwise it returns the empty string.
func (p *Part) FormName() string {
	// See http://tools.ietf.org/html/rfc2183 section 2 for EBNF
	// of Content-Disposition value format.
	if p.dispositionParams == nil {
		p.parseContentDisposition()
	}
	if p.disposition != "form-data" {
		return ""
	}
	return p.dispositionParams["name"]
}

// FileName returns the filename parameter of the Part's
// Content-Disposition header.
func (p *Part) FileName() string {
	if p.dispositionParams == nil {
		p.parseContentDisposition()
	}
	return p.dispositionParams["filename"]
}

func (p *Part) parseContentDisposition() {
	v := p.Header.Get(ContentDisposition)
	var err error
	p.disposition, p.dispositionParams, err = MIMEParseMediaType(v)
	if err != nil {
		p.dispositionParams = emptyParams
	}
}

func (bp *Part) populateHeaders() error {
	r := NewHeaderReader(bp.mr.bufReader)
	header, err := r.ReadHeader()
	if err == nil {
		bp.Header = header
	}
	return err
}

// Read reads the body of a part, after its headers and before the
// next part (if any) begins.
func (p *Part) Read(d []byte) (n int, err error) {
	return p.r.Read(d)
}

func (p *Part) Close() error {
	io.Copy(ioutil.Discard, p)
	return nil
}
