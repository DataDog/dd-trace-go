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

// isBucketNode takes in a list.Element and checks if it is a nonempty
// list Element
func isBucketNode(e *list.Element) (*list.List, bool) {
	ls, ok := e.Value.(*list.List)
	if !ok || ls == nil || ls.Front() == nil {
		return nil, false
	}
	return ls, true
}

// isSpanNode takes in a list.Element and checks if it is a non-nil
// span object
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
			// This loop should continue until it finds the an existing bucket
			// with spans that have started within `interval` nanoseconds before
			// the new span has started.
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
				if s.Start-sp.Start > interval.Nanoseconds() || sp.Start-s.Start > interval.Nanoseconds() {
					continue
				}

				for spNode != nil {
					sp, ok := isSpanNode(spNode)
					if !ok {
						continue
					}
					if s.SpanID == sp.SpanID {
						bucket.Remove(spNode)
						if bucket.Front() == nil {
							t.abandonedSpans.Remove(node)
						}
						break
					}
					spNode = spNode.Next()
				}
			}
		case <-t.stop:
			logAbandonedSpans(t.abandonedSpans, nil)
			return
		}
	}
}

// abandonedSpanString takes a span and returns a human readable string representing
// that span. If `interval` is not nil, it will check if the span is older than the
// user configured timeout, and return an empty string if it is not.
func abandonedSpanString(s *span, interval *time.Duration, curTime int64) string {
	s.Lock()
	defer s.Unlock()
	age := curTime - s.Start
	if interval != nil && age < interval.Nanoseconds() {
		return ""
	}
	a := fmt.Sprintf("%d sec", age/1e9)
	return fmt.Sprintf("[name: %s, span_id: %d, trace_id: %d, age: %s],", s.Name, s.SpanID, s.TraceID, a)
}

// abandonedBucketString takes a bucket and returns a human readable string representing
// the contents of the bucket. If `interval` is not nil, it will check if the bucket might
// contain spans older than the user configured timeout. If it does, it will filter for
// older spans. If not, it will print all spans without checking their duration.
func abandonedBucketString(bucket *list.List, interval *time.Duration, curTime int64) (int, string) {
	var sb strings.Builder
	spanCount := 0
	node := bucket.Back()
	back, ok := isSpanNode(node)
	filter := ok && interval != nil && curTime-back.Start >= interval.Nanoseconds()
	for node := bucket.Front(); node != nil; node = node.Next() {
		span, ok := isSpanNode(node)
		if !ok {
			continue
		}
		timeout := interval
		if !filter {
			timeout = nil
		}
		msg := abandonedSpanString(span, timeout, curTime)
		sb.WriteString(msg)
		spanCount++
	}
	return spanCount, sb.String()
}

// logAbandonedSpans returns a string containing potentially abandoned spans. If `interval` is
// `nil`, it will print all unfinished spans. If `interval` holds a time.Duration, it will
// only print spans that are older than `interval`. It will also truncate the log message to
// `logSize` bytes to prevent overloading the logger.
func logAbandonedSpans(l *list.List, interval *time.Duration) {
	var sb strings.Builder
	spanCount := 0
	truncated := false
	curTime := now()

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
			if curTime-sp.Start < interval.Nanoseconds() {
				continue
			}
		}
		if truncated {
			continue
		}
		nSpans, msg := abandonedBucketString(bucket, interval, curTime)
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
