// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"fmt"
	"strings"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

type AbandonedList struct {
	head *spansList
	tail *spansList
}

type spansList struct {
	head *spanNode
	tail *spanNode
	Next *spansList
}

type spanNode struct {
	Element *span
	Next    *spanNode
}

func (a *AbandonedList) String() string {
	var sb strings.Builder
	for e := a.head; e != nil; e = e.Next {
		fmt.Fprintf(&sb, "%v", e.String())
	}
	return sb.String()
}

func (s *spansList) String() string {
	var sb strings.Builder
	for e := s.head; e != nil; e = e.Next {
		fmt.Fprintf(&sb, "%v", e.String())
	}
	return sb.String()
}

func (s *spanNode) String() string {
	sp := s.Element
	if sp == nil {
		return "[],"
	}
	return fmt.Sprintf("[Span Name: %v, Span ID: %v, Trace ID: %v],", sp.Name, sp.SpanID, sp.TraceID)
}

func (a *AbandonedList) Extend() {
	n := &spansList{}
	if a.head == nil {
		a.head = n
		a.tail = n
		return
	}
	a.tail.Next = n
	a.tail = n
}

func (a *AbandonedList) RemoveTail() {
	n := a.head
	if n == nil || n.Next == nil {
		a.head = nil
		return
	}
	for n.Next.Next != nil {
		n = n.Next
	}
	n.Next = nil
	a.tail = n
}

func (a *AbandonedList) RemoveBucket(s *spansList) {
	if s.Next != nil {
		n := s.Next
		s.head = n.head
		s.Next = n.Next
		return
	}
	a.RemoveTail()
}

func (s *spansList) Append(e *span) {
	n := &spanNode{Element: e}
	if s.head == nil {
		s.head = n
		s.tail = n
		return
	}
	s.tail.Next = n
	s.tail = n
}

func (s *spansList) Remove(e *span) {
	if s.head == nil {
		return
	}
	if s.head.Element == e {
		s.head = s.head.Next
		return
	}

	n := s.head
	for n.Next != nil {
		if n.Next.Element == e {
			n.Next = n.Next.Next
			if n.Next == nil {
				s.tail = n
			}
			return
		}
		n = n.Next
	}
}

var tickerInterval = time.Minute

// reportAbandonedSpans periodically finds and reports potentially
// abandoned spans that are older than the given interval
func (t *tracer) reportAbandonedSpans(interval time.Duration) {
	tick := time.NewTicker(tickerInterval)
	defer tick.Stop()
	for {
		select {
		case <-tick.C:
			for b := t.abandonedSpans.head; b != nil; b = b.Next {
				e := b.head
				if e == nil || e.Element == nil {
					continue
				}
				if now()-e.Element.Start < interval.Nanoseconds() {
					continue
				}
				for e != nil {
					sp := e.Element
					life := now() - sp.Start
					if life >= interval.Nanoseconds() {
						log.Warn("Trace %v waiting on span %v", sp.Context().TraceID(), sp.Context().SpanID())
						e = e.Next
					} else {
						break
					}
				}
			}
		case s := <-t.cIn:
			e := t.abandonedSpans.head
			for e != nil {
				sp := e.head
				if sp == nil || sp.Element == nil {
					e = &spansList{}
					e.Append(s)
					break
				}
				if s.Start-sp.Element.Start <= interval.Nanoseconds() {
					e.Append(s)
					break
				}
				e = e.Next
			}
			if e != nil {
				break
			}
			t.abandonedSpans.Extend()
			t.abandonedSpans.tail.Append(s)
		case s := <-t.cOut:
			for e := t.abandonedSpans.head; e != nil; e = e.Next {
				if e.head == nil || e.head.Element == nil {
					continue
				}
				if s.Start-e.head.Element.Start <= interval.Nanoseconds() {
					e.Remove(s)
					if e.head == nil {
						t.abandonedSpans.RemoveBucket(e)
					}
					break
				}
			}
		case <-t.stop:
			log.Warn("Abandoned Spans: %s", t.abandonedSpans.String())
			return
		}
	}
}
