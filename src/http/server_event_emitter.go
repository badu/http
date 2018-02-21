/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package http

func (r *ServerEventEmitter) Dispatch(event ServerEventType) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := 0; i < len(r.lsns[event]); i++ {
		lisn := r.lsns[event][i]

		select {
		case lisn.ch <- event:
		default:
		}

		if lisn.once { // remove
			r.lsns[event] = append(r.lsns[event][:i], r.lsns[event][i+1:]...)
			i--
		}
	}
}

func (r *ServerEventEmitter) Once(event ServerEventType) chan ServerEventType {
	r.mu.Lock()
	defer r.mu.Unlock()
	ch := make(chan ServerEventType, 1)

	r.lsns[event] = append(r.lsns[event], EventListner{
		ch:   ch,
		once: true,
	})
	return ch
}

func (r *ServerEventEmitter) On(event ServerEventType) chan ServerEventType {
	r.mu.Lock()
	defer r.mu.Unlock()
	ch := make(chan ServerEventType, 1)

	r.lsns[event] = append(r.lsns[event], EventListner{
		ch:   ch,
		once: false,
	})
	return ch
}
