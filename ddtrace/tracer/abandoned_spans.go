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

// castAsBucketNode takes in a list.Element and checks if it is a nonempty
// list Element. If it can be cast as a list.List, that list will be returned
// bool flag `true`. If not, it will return `nil` with bool flag `false`
func castAsBucketNode(e *list.Element) (*list.List, bool) {
	if e == nil {
		return nil, false
	}
	ls, ok := e.Value.(*list.List)
	if !ok || ls == nil || ls.Front() == nil {
		return nil, false
	}
	return ls, true
}

// castAsSpanNode takes in a list.Element and checks if it is a non-nil
// span object. If it can be cast as a span, that span will be returned
// bool flag `true`. If not, it will return `nil` with bool flag `false`
func castAsSpanNode(e *list.Element) (*span, bool) {
	if e == nil {
		return nil, false
	}
	s, ok := e.Value.(*span)
	if !ok || s == nil {
		return nil, false
	}
	return s, true
}

// findSpanBucket takes in a start time in Unix Nanoseconds and the user
// configured interval, then finds the bucket that the given span should
// belong in. All spans within the same bucket should have a start time that
// is within `interval` nanoseconds of each other.
func (t *tracer) findSpanBucket(start int64, interval time.Duration) (*list.Element, bool) {
	for node := t.abandonedSpans.Front(); node != nil; node = node.Next() {
		bucket, ok := castAsBucketNode(node)
		if !ok {
			continue
		}
		for spNode := bucket.Front(); spNode != nil; spNode = spNode.Next() {
			sp, ok := castAsSpanNode(spNode)
			if !ok {
				continue
			}
			if start >= sp.Start && start-sp.Start < interval.Nanoseconds() {
				return node, true
			}
		}
	}
	return nil, false
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
			bNode, bOk := t.findSpanBucket(s.Start, interval)
			bucket, ok := castAsBucketNode(bNode)
			if bOk && ok {
				bucket.PushBack(s)
				break
			}
			// If no matching bucket exists, create a new one and append the new
			// span to the top of the bucket.
			b := list.New()
			b.PushBack(s)
			t.abandonedSpans.PushBack(b)
		case s := <-t.cOut:
			bNode, bOk := t.findSpanBucket(s.Start, interval)
			bucket, ok := castAsBucketNode(bNode)
			if !bOk || !ok {
				break
			}
			// If a matching bucket exists, attempt to find the element containing
			// the finished span, then remove that element from the bucket.
			// If a bucket becomes empty, also remove that bucket from the
			// abandoned spans list.
			for node := bucket.Front(); node != nil; node = node.Next() {
				sp, sOk := castAsSpanNode(node)
				if !sOk {
					continue
				}
				if sp.SpanID != s.SpanID {
					continue
				}
				bucket.Remove(node)
				if bucket.Front() == nil {
					t.abandonedSpans.Remove(bNode)
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
	back, ok := castAsSpanNode(node)
	filter := ok && interval != nil && curTime-back.Start >= interval.Nanoseconds()
	for node := bucket.Front(); node != nil; node = node.Next() {
		span, ok := castAsSpanNode(node)
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
		bucket, ok := castAsBucketNode(bucketNode)
		if !ok {
			continue
		}

		// since spans are bucketed by time, finding a bucket that is newer
		// than the allowed time interval means that all spans in this bucket
		// and future buckets will be younger than `interval`, and thus aren't
		// worth checking.
		if interval != nil {
			spanNode := bucket.Front()
			sp, ok := castAsSpanNode(spanNode)
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
