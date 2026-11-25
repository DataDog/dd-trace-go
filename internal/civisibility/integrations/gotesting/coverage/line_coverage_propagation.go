// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package coverage

import "github.com/DataDog/dd-trace-go/v2/internal/log"

// A request that a test sends to a server in the same process is part of the
// test's own work, like the goroutines it starts. The server's goroutines
// inherit the slot of whoever started the server: a parent test, or no test
// when TestMain or a package initialiser started it. So the client integration
// reports each request a window's goroutine sends, under the span ID it
// injects, and the server integration that extracts that span ID moves the
// handler's goroutine into the window's slot while it handles the request.

// sentRequest is a request that a goroutine of an open window sent.
type sentRequest struct {
	slot   uint32
	window *LineCoverageWindow
}

// Sent records that the calling goroutine sent a request carrying the span
// context of spanID, when the goroutine works for an open window.
func (c *lineCoverageCollector) Sent(spanID uint64) (forget func()) {
	defer func() {
		if r := recover(); r != nil {
			log.Error("civisibility.linecov: recording a request failed: %v", r)
			forget = noop
		}
	}()
	slot := runtimeLineCoverageSlot()
	if spanID == 0 || slot == 0 || slot >= lineCoverageSlotCount {
		return noop
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	w := c.windows[slot]
	if w == nil || w.closing {
		return noop
	}
	if c.requests == nil {
		c.requests = map[uint64]sentRequest{}
	}
	c.requests[spanID] = sentRequest{slot: slot, window: w}
	return func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.requests[spanID].window == w {
			delete(c.requests, spanID)
		}
	}
}

// Received moves the calling goroutine into the slot of the window whose
// goroutine sent the request that carries spanID, while that window is open.
// The returned function moves the goroutine back to the slot it had, or to
// slot 0 when the window of that slot has closed since.
func (c *lineCoverageCollector) Received(spanID uint64) (restore func()) {
	defer func() {
		if r := recover(); r != nil {
			log.Error("civisibility.linecov: moving a request handler failed: %v", r)
			restore = noop
		}
	}()
	c.mu.Lock()
	defer c.mu.Unlock()
	req, ok := c.requests[spanID]
	if !ok || c.windows[req.slot] != req.window || req.window.closing {
		return noop
	}
	prev := runtimeLineCoverageSlot()
	if prev == req.slot {
		return noop
	}
	var prevWindow *LineCoverageWindow
	if prev < lineCoverageSlotCount {
		prevWindow = c.windows[prev]
	}
	runtimeLineCoverageSetSlot(req.slot)
	return func() {
		defer func() {
			if r := recover(); r != nil {
				log.Error("civisibility.linecov: moving a request handler back failed: %v", r)
			}
		}()
		c.mu.Lock()
		defer c.mu.Unlock()
		if runtimeLineCoverageSlot() != req.slot {
			// The window closed and moved the goroutine to slot 0, or this
			// runs on another goroutine.
			return
		}
		back := uint32(0)
		if prevWindow != nil && c.windows[prev] == prevWindow && !prevWindow.closing {
			back = prev
		}
		runtimeLineCoverageSetSlot(back)
	}
}
