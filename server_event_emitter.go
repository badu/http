/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package http

func (r *srvEvDispatcher) Dispatch(event ServerEventType) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := 0; i < len(r.lsns[event]); i++ {
		lisn := r.lsns[event][i]
		select {
		case lisn.ch <- event:
		default:
		}
	}
}

func (r *srvEvDispatcher) on(event ServerEventType) chan ServerEventType {
	r.mu.Lock()
	defer r.mu.Unlock()
	ch := make(chan ServerEventType, 1)
	r.lsns[event] = append(r.lsns[event], srvEvListner{ch: ch})
	return ch
}

func (h ServerEventHandler) Next() {
	h.Add(1)
	go func() {
		defer h.Done()
		func() {
			switch <-h.ch {
			case h.eventType:
				h.handler()
			case killListeners:
				// on kill, we will not do "next" execution
				h.willRemount = false
			}
		}()
	}()
	h.Wait()
	if h.willRemount {
		// next execution
		go h.Next()
	}
}

func (h ServerEventHandler) Kill() {
	h.ch <- killListeners
}

func ListenTestEvent(eventType ServerEventType, f func()) ServerEventHandler {
	wg := ServerEventHandler{ch: TestEventsEmitter.on(eventType), handler: f, eventType: eventType, willRemount: true}
	// first execution
	go wg.Next()
	return wg
}
