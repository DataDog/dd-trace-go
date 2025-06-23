// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package gocontrolplane

import (
	"github.com/DataDog/dd-trace-go/contrib/envoyproxy/go-control-plane/v2/internal/json"
)

type BodyBufferPhase int

const (
	RequestBodyPhase BodyBufferPhase = iota
	ResponseBodyPhase
)

// BodyBuffer manages the buffering of request/response bodies with size limits
type BodyBuffer struct {
	buffer    []byte
	sizeLimit int
	truncated bool
	phase     BodyBufferPhase
}

// NewBodyBuffer creates a new BodyBuffer with the specified size limit
func NewBodyBuffer(sizeLimit int) *BodyBuffer {
	return &BodyBuffer{
		sizeLimit: sizeLimit,
		truncated: false,
	}
}

// Append adds a chunk of data to the buffer, respecting the size limit
func (b *BodyBuffer) Append(chunk []byte) {
	if b.truncated || len(chunk) == 0 {
		return
	}

	currentSize := len(b.buffer)
	remainingCapacity := b.sizeLimit - currentSize

	if remainingCapacity <= 0 {
		b.truncated = true
		return
	}

	bytesToAdd := len(chunk)
	if bytesToAdd > remainingCapacity {
		bytesToAdd = remainingCapacity
		b.truncated = true
		instr.Logger().Debug("external_processing: body size limit reached, truncating body to %d bytes", bytesToAdd)
	}

	if b.buffer == nil {
		b.buffer = make([]byte, 0, bytesToAdd)
	}

	b.buffer = append(b.buffer, chunk[:bytesToAdd]...)
}

// IsComplete returns true if the buffer has been truncated or reached capacity
func (b *BodyBuffer) IsComplete() bool {
	return b.truncated
}

// IsTruncated returns true if the buffer was truncated due to size limits
func (b *BodyBuffer) IsTruncated() bool {
	return b.truncated
}

// GetJSONEncodable returns a JSON encodable representation of the buffer
func (b *BodyBuffer) GetJSONEncodable() *json.Encodable {
	return json.NewEncodable(b.buffer, b.truncated)
}

// GetPhase returns the current phase of the body buffer
func (b *BodyBuffer) GetPhase() BodyBufferPhase {
	return b.phase
}

// Reset clears the buffer for reuse
func (b *BodyBuffer) Reset(phase BodyBufferPhase) {
	b.buffer = nil
	b.truncated = false
	b.phase = phase
}
