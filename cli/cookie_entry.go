/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package cli

import (
	"fmt"
)

// id returns the domain;path;name triple of e as an id.
func (e *cookieEntry) id() string {
	return fmt.Sprintf("%s;%s;%s", e.Domain, e.Path, e.Name)
}

// shouldSend determines whether e's cookie qualifies to be included in a
// request to host/path. It is the caller's responsibility to check if the
// cookie is expired.
func (e *cookieEntry) shouldSend(https bool, host, path string) bool {
	return e.domainMatch(host) && e.pathMatch(path) && (https || !e.Secure)
}

// domainMatch implements "domain-match" of RFC 6265 section 5.1.3.
func (e *cookieEntry) domainMatch(host string) bool {
	if e.Domain == host {
		return true
	}
	return !e.HostOnly && hasDotSuffix(host, e.Domain)
}

// pathMatch implements "path-match" according to RFC 6265 section 5.1.4.
func (e *cookieEntry) pathMatch(requestPath string) bool {
	if requestPath == e.Path {
		return true
	}
	//@comment : was `if strings.HasPrefix(requestPath, e.Path) {`
	le := len(e.Path)
	if len(requestPath) >= le && requestPath[:le] == e.Path {
		if e.Path[len(e.Path)-1] == '/' {
			return true // The "/any/" matches "/any/path" case.
		} else if requestPath[len(e.Path)] == '/' {
			return true // The "/any" matches "/any/path" case.
		}
	}
	return false
}
