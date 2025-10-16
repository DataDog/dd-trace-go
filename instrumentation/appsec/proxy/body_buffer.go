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

	if b.buffer == nil {
		b.buffer = make([]byte, 0, bytesToAdd)
	}

	b.buffer = append(b.buffer, chunk[:bytesToAdd]...)
}
