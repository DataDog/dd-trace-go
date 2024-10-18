// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package newtelemetry

import (
	"sync"
)

const maxRingBufferSize = 1 << 14
const startingRingBufferSize = 1 << 8

type RingQueue[T any] struct {
	// buffer is the slice that contains the data.
	buffer []T
	// head is the index of the first element in the buffer.
	head int
	// tail is the index of the last element in the buffer.
	tail int
	// mu is the lock for the buffer, head and tail.
	mu sync.Mutex
	// pool is the pool of buffers. Normally there should only be one or 2 buffers in the pool.
	pool sync.Pool
}

func NewRingQueue[T any]() *RingQueue[T] {
	return &RingQueue[T]{
		buffer: make([]T, startingRingBufferSize),
		pool: sync.Pool{
			New: func() any { return make([]T, startingRingBufferSize) },
		},
	}
}

// Enqueue adds a value to the buffer.
func (rb *RingQueue[T]) Enqueue(val T) {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	rb.buffer[rb.tail] = val
	rb.tail = (rb.tail + 1) % len(rb.buffer)

	if rb.tail == rb.head && len(rb.buffer) == maxRingBufferSize { // We loose one element
		rb.head = (rb.head + 1) % len(rb.buffer)
		// TODO: maybe log that we lost an element
		return
	}

	// We need to resize the buffer, we double the size, this should happen 10 times before we reach the max size
	if rb.tail == rb.head {
		newBuffer := make([]T, cap(rb.buffer)*2)
		copy(newBuffer, rb.buffer[rb.head:])
		copy(newBuffer[len(rb.buffer)-rb.head:], rb.buffer[:rb.tail])
		rb.head = 0
		rb.tail = len(rb.buffer) - 1
		rb.buffer = newBuffer
	}
}

// Dequeue removes a value from the buffer.
func (rb *RingQueue[T]) Dequeue() T {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	val := rb.buffer[rb.head]
	rb.head = (rb.head + 1) % len(rb.buffer)
	return val
}

// GetBuffer returns the current buffer and resets it.
func (rb *RingQueue[T]) GetBuffer() []T {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	prevBuf := rb.buffer
	rb.buffer = rb.pool.Get().([]T)
	rb.head = 0
	rb.tail = len(rb.buffer) - 1
	return prevBuf
}

// ReleaseBuffer returns the buffer to the pool.
func (rb *RingQueue[T]) ReleaseBuffer(buf []T) {
	rb.pool.Put(buf)
}

// IsEmpty returns true if the buffer is empty.
func (rb *RingQueue[T]) IsEmpty() bool {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	return rb.head == rb.tail
}

// IsFull returns true if the buffer is full and cannot accept more elements.
func (rb *RingQueue[T]) IsFull() bool {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	return (rb.tail+1)%len(rb.buffer) == rb.head && len(rb.buffer) == maxRingBufferSize
}
