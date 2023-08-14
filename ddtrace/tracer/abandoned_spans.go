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

func isBucketNode(e *list.Element) (*list.List, bool) {
	ls, ok := e.Value.(*list.List)
	if !ok || ls == nil || ls.Front() == nil {
		return nil, false
	}
	return ls, true
}

func isSpanNode(e *list.Element) (*span, bool) {
	s, ok := e.Value.(*span)
	if !ok || s == nil {
		return nil, false
	}
	return s, true
}

// reportAbandonedSpans periodically finds and reports potentially abandoned
// spans that are older than the given interval. These spans are stored in a
// bucketed linked list, sorted by their `Start` time, where the front of the
// list contains the oldest spans, and the end of the list contains the newest spans.
func (t *tracer) reportAbandonedSpans(interval time.Duration) {
	tick := time.NewTicker(tickerInterval)
	defer tick.Stop()
	for {
		select {
		case <-tick.C:
			logAbandonedSpans(t.abandonedSpans, &interval)
		case s := <-t.cIn:
			bNode := t.abandonedSpans.Front()
			if bNode == nil {
				b := list.New()
				b.PushBack(s)
				t.abandonedSpans.PushBack(b)
				break
			}
			// All spans within the same bucket should have a start time that
			// is within `interval` nanoseconds of each other.
			// This loop should continue until the correct bucket is found. This
			// includes empty or nil buckets (which have no spans in them) or
			// an existing bucket with spans that have started within `interval`
			// nanoseconds before the new span has started.
			for bNode != nil {
				bucket, bOk := isBucketNode(bNode)
				if !bOk {
					bNode = bNode.Next()
					continue
				}
				sNode := bucket.Front()
				if sNode == nil {
					bNode = bNode.Next()
					continue
				}
				sp, sOk := isSpanNode(sNode)
				if sOk && s.Start-sp.Start <= interval.Nanoseconds() {
					bucket.PushBack(s)
					break
				}
				bNode = bNode.Next()
			}
			if bNode != nil {
				break
			}
			// If no matching bucket exists, create a new one and append the new
			// span to the top of the bucket.
			b := list.New()
			b.PushBack(s)
			t.abandonedSpans.PushBack(b)
		case s := <-t.cOut:
			// This loop should continue until it finds the bucket with spans
			// starting within `interval` nanoseconds of the finished span,
			// then remove that span from the bucket.
			for node := t.abandonedSpans.Front(); node != nil; node = node.Next() {
				bucket, ok := isBucketNode(node)
				if !ok {
					continue
				}
				spNode := bucket.Front()
				sp, ok := isSpanNode(spNode)
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
			logAbandonedSpans(t.abandonedSpans, nil)
			return
		}
	}
}

func abandonedSpanString(s *span, interval *time.Duration) string {
	s.Lock()
	defer s.Unlock()
	return fmt.Sprintf("[name: %s, span_id: %d, trace_id: %d, age: %d],", s.Name, s.SpanID, s.TraceID, s.Duration)
}

func abandonedBucketString(bucket *list.List, interval *time.Duration) (int, string) {
	var sb strings.Builder
	spanCount := 0
	node := bucket.Back()
	span, ok := isSpanNode(node)
	filter := ok && interval != nil && now()-span.Start <= interval.Nanoseconds()
	for node := bucket.Front(); node != nil; node = node.Next() {
		span, ok := isSpanNode(node)
		if !ok {
			continue
		}
		var msg string
		if filter {
			msg = abandonedSpanString(span, interval)
		} else {
			msg = abandonedSpanString(span, nil)
		}
		sb.WriteString(msg)
		spanCount++
	}
	return spanCount, sb.String()
}

// logAbandonedSpans returns a string containing potentially abandoned spans. If `filter` is true,
// it will only return spans that are older than the provided time `interval`. If false,
// it will return all unfinished spans.
func logAbandonedSpans(l *list.List, interval *time.Duration) {
	var sb strings.Builder
	nowTime := now()
	spanCount := 0
	truncated := false

	for bucketNode := l.Front(); bucketNode != nil; bucketNode = bucketNode.Next() {
		bucket, ok := isBucketNode(bucketNode)
		if !ok {
			continue
		}

		// since spans are bucketed by time, finding a bucket that is newer
		// than the allowed time interval means that all spans in this bucket
		// and future buckets will be younger than `interval`, and thus aren't
		// worth checking.
		if interval != nil {
			spanNode := bucket.Front()
			sp, ok := isSpanNode(spanNode)
			if !ok {
				continue
			}
			if nowTime-sp.Start < interval.Nanoseconds() {
				continue
			}
		}
		if truncated {
			continue
		}
		nSpans, msg := abandonedBucketString(bucket, interval)
		spanCount += nSpans
		space := logSize - len(sb.String())
		if len(msg) > space {
			msg = msg[0:space]
			truncated = true
		}
		sb.WriteString(msg)
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
