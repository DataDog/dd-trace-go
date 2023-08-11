// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"container/list"
	"fmt"
	"strings"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

var tickerInterval = time.Minute
var logSize = 9000

// reportAbandonedSpans periodically finds and reports potentially abandoned
// spans that are older than the given interval. These spans are stored in a
// bucketed linked list, sorted by their `Start` time.
func (t *tracer) reportAbandonedSpans(interval time.Duration) {
	tick := time.NewTicker(tickerInterval)
	defer tick.Stop()
	for {
		select {
		case <-tick.C:
			logAbandonedSpans(t.abandonedSpans, true, interval)
		case s := <-t.cIn:
			bNode := t.abandonedSpans.Front()
			if bNode == nil {
				b := list.New()
				b.PushBack(s)
				t.abandonedSpans.PushBack(b)
				break
			}
			for bNode != nil {
				bucket, bOk := bNode.Value.(*list.List)
				if !bOk {
					bNode = bNode.Next()
					continue
				}
				sNode := bucket.Front()
				if sNode == nil {
					bNode = bNode.Next()
					continue
				}
				sp, sOk := sNode.Value.(*span)
				if sOk && sp != nil && s.Start-sp.Start <= interval.Nanoseconds() {
					bucket.PushBack(s)
					break
				}
				bNode = bNode.Next()
			}
			if bNode != nil {
				break
			}
			b := list.New()
			b.PushBack(s)
			t.abandonedSpans.PushBack(b)
		case s := <-t.cOut:
			for node := t.abandonedSpans.Front(); node != nil; node = node.Next() {
				bucket, ok := node.Value.(*list.List)
				if !ok || bucket.Front() == nil {
					continue
				}
				spNode := bucket.Front()
				sp, ok := spNode.Value.(*span)
				if !ok {
					continue
				}
				if s.Start-sp.Start <= interval.Nanoseconds() {
					bucket.Remove(spNode)
					if bucket.Front() == nil {
						t.abandonedSpans.Remove(node)
					}
					break
				}
			}
		case <-t.stop:
			logAbandonedSpans(t.abandonedSpans, false, interval)
			return
		}
	}
}

// logAbandonedSpans returns a string containing potentially abandoned spans. If `filter` is true,
// it will only return spans that are older than the provided time `interval`. If false,
// it will return all unfinished spans.
func logAbandonedSpans(l *list.List, filter bool, interval time.Duration) {
	var sb strings.Builder
	nowTime := now()
	spanCount := 0
	truncated := false

	for bucketNode := l.Front(); bucketNode != nil; bucketNode = bucketNode.Next() {
		bucket, ok := bucketNode.Value.(*list.List)
		if !ok || bucket == nil {
			continue
		}

		// since spans are bucketed by time, finding a bucket that is newer
		// than the allowed time interval means that all spans in this bucket
		// and future buckets will be younger than `interval`, and thus aren't
		// worth checking.
		if filter {
			spanNode := bucket.Front()
			if spanNode == nil {
				continue
			}
			sp, ok := spanNode.Value.(*span)
			if !ok || sp == nil {
				continue
			}
			if nowTime-sp.Start < interval.Nanoseconds() {
				continue
			}
		}
		for spanNode := bucket.Front(); spanNode != nil; spanNode = spanNode.Next() {
			sp, ok := spanNode.Value.(*span)
			if !ok || sp == nil {
				continue
			}

			// despite quitting early, spans within the same bucket can still fall on either side
			// of the timeout. We should still check if the span is too old or not.
			if filter && nowTime-sp.Start < interval.Nanoseconds() {
				break
			}
			msg := fmt.Sprintf("[name: %s, span_id: %d, trace_id: %d, age: %d],", sp.Name, sp.SpanID, sp.TraceID, sp.Duration)
			spanCount++
			if logSize-len(sb.String()) < len(msg) {
				truncated = true
				continue
			}

			sb.WriteString(msg)
		}
	}

	if spanCount == 0 {
		return
	}

	log.Warn("%d abandoned spans:", spanCount)
	if truncated {
		log.Warn("Too many abandoned spans. Truncating message.")
		sb.WriteString("...")
	}
	log.Warn(sb.String())
}
