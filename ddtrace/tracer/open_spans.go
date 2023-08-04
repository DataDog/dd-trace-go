// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

type BLList[T comparable] struct {
	mu   sync.Mutex
	head *listNode[*LList[T]]
	tail *listNode[*LList[T]]
}

type LList[T comparable] struct {
	mu   sync.Mutex
	head *listNode[T]
	tail *listNode[T]
}

type listNode[T comparable] struct {
	Element T
	Next    *listNode[T]
}

func (b *BLList[T]) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	var sb strings.Builder
	for e := b.head; e != nil; e = e.Next {
		fmt.Fprintf(&sb, "%v", e.String())
	}
	return sb.String()
}

func (l *LList[T]) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	var sb strings.Builder
	for e := l.head; e != nil; e = e.Next {
		fmt.Fprintf(&sb, "%v", e.String())
	}
	return sb.String()
}

func (n *listNode[T]) String() string {
	return fmt.Sprintf("[%v]", n.Element)
}

func (b *BLList[T]) Extend() {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := &listNode[*LList[T]]{
		Element: &LList[T]{},
		Next:    nil,
	}
	if b.head == nil {
		b.head = n
		b.tail = n
		return
	}
	b.tail.Next = n
	b.tail = n
}

func (b *BLList[T]) RemoveTail() {
	n := b.head
	if n == nil || n.Next == nil {
		b.head = nil
		return
	}
	for n.Next.Next != nil {
		n = n.Next
	}
	n.Next = nil
	b.tail = n
}

func (b *BLList[T]) RemoveBucket(l *listNode[*LList[T]]) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if l.Next != nil {
		n := l.Next
		l.Element = n.Element
		l.Next = n.Next
		return
	}
	b.RemoveTail()
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
						e = e.Next
					} else {
						break
					}
				}
				b = b.Next
			}
		case s := <-t.cIn:
			e := t.openSpans.head
			for e != nil {
				sp := e.Element.head
				if sp == nil || sp.Element == nil {
					e.Element = &LList[*span]{}
					e.Element.Append(s)
					break
				}
				if s.Start-sp.Element.Start <= interval.Nanoseconds() {
					e.Element.Append(s)
					break
				}
				e = e.Next
			}
			t.openSpans.Extend()
			t.openSpans.tail.Element.Append(s)
		case s := <-t.cOut:
			for e := t.openSpans.head; e != nil; e = e.Next {
				if e.Element.head == nil {
					continue
				}
				if s.Start-e.Element.head.Element.Start <= interval.Nanoseconds() {
					e.Element.Remove(s)
					if e.Element.head == nil {
						t.openSpans.RemoveBucket(e)
					}
					break
				}
			}
		case <-t.stop:
			return
		}
	}
}
