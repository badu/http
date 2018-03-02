/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package url

// Username returns the username.
func (u *Userinfo) Username() string {
	if u == nil {
		return ""
	}
	return u.username
}

// Password returns the password in case it is set, and whether it is set.
func (u *Userinfo) Password() (string, bool) {
	if u == nil {
		return "", false
	}
	return u.password, u.passwordSet
}

// String returns the encoded userinfo information in the standard form
// of "username[:password]".
func (u *Userinfo) String() string {
	if u == nil {
		return ""
	}
	s := escape(u.username, encodeUserPassword)
	if u.passwordSet {
		s += ":" + escape(u.password, encodeUserPassword)
	}
	return s
}
