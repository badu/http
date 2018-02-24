/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package http

import "log"

func (c *loggingConn) Write(p []byte) (int, error) {
	log.Printf("%s.Write(%d) = ....", c.name, len(p))
	n, err := c.Conn.Write(p)
	log.Printf("%s.Write(%d) = %d, %v", c.name, len(p), n, err)
	return n, err
}

func (c *loggingConn) Read(p []byte) (int, error) {
	log.Printf("%s.Read(%d) = ....", c.name, len(p))
	n, err := c.Conn.Read(p)
	log.Printf("%s.Read(%d) = %d, %v", c.name, len(p), n, err)
	return n, err
}

func (c *loggingConn) Close() error {
	log.Printf("%s.Close() = ...", c.name)
	err := c.Conn.Close()
	log.Printf("%s.Close() = %v", c.name, err)
	return err
}
