// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

// reportOpenSpans periodically finds and reports old, open spans at
// the given interval.
func (t *tracer) reportOpenSpans(interval time.Duration) {
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		select {
		case <-tick.C:
			// check for open spans
			for e := t.openSpans.Front(); e != nil; e = e.Next() {
				sp := e.Value.(*span)
				life := now() - sp.Start
				if life >= interval.Nanoseconds() {
					log.Debug("Trace %v waiting on span %v", sp.Context().TraceID(), sp.Context().SpanID())
				}
			}
		case s := <-t.cIn:
			t.openSpans.PushBack(s)
		case s := <-t.cOut:
			for e := t.openSpans.Front(); e != nil; e = e.Next() {
				if e.Value == s {
					t.openSpans.Remove(e)
					break
				}
			}
		case <-t.stop:
			return
		}
	}
}
