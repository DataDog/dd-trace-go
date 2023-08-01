// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"sync"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

type LList[T comparable] struct {
	mu   sync.Mutex
	head *listNode[T]
	tail *listNode[T]
}

type listNode[T comparable] struct {
	Element T
	Next    *listNode[T]
}

func (l *LList[T]) Append(e T) {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := &listNode[T]{Element: e}
	if l.head == nil {
		l.head = n
		l.tail = n
		return
	}
	l.tail.Next = n
	l.tail = n
}

func (l *LList[T]) Remove(e T) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.head == nil {
		return
	}
	if l.head.Element == e {
		l.head = l.head.Next
		return
	}

	n := l.head
	for n.Next != nil {
		if n.Next.Element == e {
			n.Next = n.Next.Next
			return
		}
		n = n.Next
	}
}

func (l *LList[T]) RemoveTail() {
	n := l.head
	if n == nil || n.Next == nil {
		l.head = nil
		return
	}
	for n.Next.Next != nil {
		n = n.Next
	}
	n.Next = nil
	l.tail = n
}

func (l *LList[T]) RemoveNode(s *listNode[T]) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if s.Next != nil {
		n := s.Next
		s.Element = n.Element
		s.Next = n.Next
		return
	}
	l.RemoveTail()
}

// reportOpenSpans periodically finds and reports old, open spans at
// the given interval.
func (t *tracer) reportOpenSpans(interval time.Duration) {
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		select {
		case <-tick.C:
			// check for open spans
			e := t.openSpans.head
			for e != nil {
				sp := e.Element
				life := now() - sp.Start
				if life >= interval.Nanoseconds() {
					log.Warn("Trace %v waiting on span %v", sp.Context().TraceID(), sp.Context().SpanID())
					t.openSpans.RemoveNode(e)
					e = t.openSpans.head
				} else {
					break
				}
			}
		case s := <-t.cIn:
			t.openSpans.Append(s)
		case s := <-t.cOut:
			t.openSpans.Remove(s)
		case <-t.stop:
			return
		}
	}
}
