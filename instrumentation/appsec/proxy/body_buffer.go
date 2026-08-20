// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package proxy

// bodyBuffer manages the buffering of request/response bodies with size limits
type bodyBuffer struct {
	buffer    []byte
	sizeLimit int
	truncated bool
	analyzed  bool
}

// newBodyBuffer creates a new bodyBuffer with the specified size limit
func newBodyBuffer(sizeLimit int) *bodyBuffer {
	return &bodyBuffer{
		sizeLimit: sizeLimit,
		truncated: false,
	}
}

// append adds a chunk of data to the buffer, respecting the size limit
func (b *bodyBuffer) append(chunk []byte) {
	if b.truncated || len(chunk) == 0 {
		return
	}

	currentSize := len(b.buffer)
	remainingCapacity := b.sizeLimit - currentSize

	bytesToAdd := len(chunk)
	if bytesToAdd > remainingCapacity {
		bytesToAdd = remainingCapacity
		b.truncated = true
	}

	b.grow(currentSize + bytesToAdd)
	b.buffer = append(b.buffer, chunk[:bytesToAdd]...)
}

// minBodyBufferCapacity is the smallest allocation worth making. A streamed body
// arrives in many small chunks, and starting at the size of the first one costs a
// reallocation and a full copy for every chunk that follows.
const minBodyBufferCapacity = 4096

// grow ensures the buffer can hold size bytes without reallocating.
//
// Capacity is doubled to keep appends amortized, then clamped to sizeLimit, which
// append cannot do on its own: left to itself it rounds the final chunk of a large
// body up past the limit we promised to respect. Bodies never exceed sizeLimit, so
// clamping only ever removes waste.
func (b *bodyBuffer) grow(size int) {
	if cap(b.buffer) >= size {
		return
	}

	capacity := max(2*cap(b.buffer), size, minBodyBufferCapacity)
	capacity = min(capacity, b.sizeLimit)

	grown := make([]byte, len(b.buffer), capacity)
	copy(grown, b.buffer)
	b.buffer = grown
}
