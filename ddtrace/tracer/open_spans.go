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
			b := t.openSpans.head
			for b != nil {
				e := b.Element.head
				for e != nil {
					sp := e.Element
					life := now() - sp.Start
					if life >= interval.Nanoseconds() {
						log.Warn("Trace %v waiting on span %v", sp.Context().TraceID(), sp.Context().SpanID())
						b.Element.RemoveNode(e)
						e = b.Element.head
					} else {
						break
					}
				}
				b = b.Next
			}
		case s := <-t.cIn:
			e := t.openSpans.head
			if e == nil {
				t.openSpans.head = &listNode[LList[*span]]{
					Element: LList[*span]{},
				}
				t.openSpans.head.Element.Append(s)
				break
			}
			for e != nil {
				sp := e.Element.head
				if sp == nil || sp.Element == nil {
					e.Element.head = &listNode[*span]{
						Element: s,
					}
					break
				}
				if s.Start-sp.Element.Start <= interval.Nanoseconds() {
					e.Element.Append(s)
					break
				}
			}
		case s := <-t.cOut:
			for e := t.openSpans.head; e != nil; e = e.Next {
				if e.Element.head == nil {
					continue
				}
				if s.Start-e.Element.head.Element.Start <= interval.Nanoseconds() {
					e.Element.Remove(s)
					if e.Element.head == nil {
						t.openSpans.RemoveNode(e)
					}
					break
				}
			}
		case <-t.stop:
			return
		}
	}
}
