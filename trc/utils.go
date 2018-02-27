/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package trc

import (
	"context"
	"fmt"
	"net"
	"os"
)

// ContextClientTrace returns the ClientTrace associated with the
// provided context. If none, it returns nil.
func ContextClientTrace(ctx context.Context) *ClientTrace {
	trace, _ := ctx.Value(clientEventContextKey{}).(*ClientTrace)
	return trace
}

// WithClientTrace returns a new context based on the provided parent
// ctx. HTTP client requests made with the returned context will use
// the provided trace hooks, in addition to any previous hooks
// registered with ctx. Any hooks defined in the provided trace will
// be called first.
func WithClientTrace(ctx context.Context, tracer *ClientTrace) context.Context {
	if tracer == nil {
		panic("nil tracer")
	}
	old := ContextClientTrace(ctx)
	tracer.compose(old)

	ctx = context.WithValue(ctx, clientEventContextKey{}, tracer)
	if tracer.hasNetHooks() {
		nt := &Trace{
			ConnectStart: tracer.ConnectStart,
			ConnectDone:  tracer.ConnectDone,
		}
		if tracer.DNSStart != nil {
			nt.DNSStart = func(name string) {
				tracer.DNSStart(DNSStartInfo{Host: name})
			}
		}
		if tracer.DNSDone != nil {
			nt.DNSDone = func(netIPs []interface{}, coalesced bool, err error) {
				addrs := make([]net.IPAddr, len(netIPs))
				for i, ip := range netIPs {
					addrs[i] = ip.(net.IPAddr)
				}
				tracer.DNSDone(DNSDoneInfo{
					Addrs:     addrs,
					Coalesced: coalesced,
					Err:       err,
				})
			}
		}
		ctx = context.WithValue(ctx, TraceKey{}, nt)
	} else {
		fmt.Fprintf(os.Stderr, "No hooks declared in tracer : %v\n", tracer)
	}
	return ctx
}
