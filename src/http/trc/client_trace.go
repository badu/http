/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package trc

import (
	"reflect"
)

// compose modifies t such that it respects the previously-registered hooks in old,
// subject to the composition policy requested in t.Compose.
func (t *ClientTrace) compose(old *ClientTrace) {
	if old == nil {
		return
	}
	tv := reflect.ValueOf(t).Elem()
	ov := reflect.ValueOf(old).Elem()
	structType := tv.Type()
	for i := 0; i < structType.NumField(); i++ {
		tf := tv.Field(i)
		hookType := tf.Type()
		if hookType.Kind() != reflect.Func {
			continue
		}
		of := ov.Field(i)
		if of.IsNil() {
			continue
		}
		if tf.IsNil() {
			tf.Set(of)
			continue
		}

		// Make a copy of tf for tf to call. (Otherwise it
		// creates a recursive call cycle and stack overflows)
		tfCopy := reflect.ValueOf(tf.Interface())

		// We need to call both tf and of in some order.
		newFunc := reflect.MakeFunc(hookType, func(args []reflect.Value) []reflect.Value {
			tfCopy.Call(args)
			return of.Call(args)
		})
		tv.Field(i).Set(newFunc)
	}
}

func (t *ClientTrace) hasNetHooks() bool {
	if t == nil {
		return false
	}
	//TODO : @badu - condition was : t.DNSStart != nil || t.DNSDone != nil || t.ConnectStart != nil || t.ConnectDone != nil but transport_test.go was expecting to execute TLSHandshakeStart, TLSHandshakeDone, PutIdleConn
	return t.DNSStart != nil ||
		t.DNSDone != nil ||
		t.ConnectStart != nil ||
		t.ConnectDone != nil ||
		t.TLSHandshakeStart != nil ||
		t.TLSHandshakeDone != nil ||
		t.PutIdleConn != nil
}
