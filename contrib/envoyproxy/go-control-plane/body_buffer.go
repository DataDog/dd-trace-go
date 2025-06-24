// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package gocontrolplane

type BodyBufferPhase int

const (
	RequestBodyPhase BodyBufferPhase = iota
	ResponseBodyPhase
)

// bodyBuffer manages the buffering of request/response bodies with size limits
type bodyBuffer struct {
	Buffer    []byte
	SizeLimit int
	Truncated bool
	Phase     BodyBufferPhase
}

// newBodyBuffer creates a new bodyBuffer with the specified size limit
func newBodyBuffer(sizeLimit int) *bodyBuffer {
	return &bodyBuffer{
		SizeLimit: sizeLimit,
		Truncated: false,
	}
}

// Append adds a chunk of data to the buffer, respecting the size limit
func (b *bodyBuffer) Append(chunk []byte) {
	if b.Truncated || len(chunk) == 0 {
		return
	}

	currentSize := len(b.Buffer)
	remainingCapacity := b.SizeLimit - currentSize

	if remainingCapacity <= 0 {
		b.Truncated = true
		return
	}

	bytesToAdd := len(chunk)
	if bytesToAdd > remainingCapacity {
		bytesToAdd = remainingCapacity
		b.Truncated = true
		instr.Logger().Debug("external_processing: body size limit reached, truncating body to %d bytes", bytesToAdd)
	}

	if b.Buffer == nil {
		b.Buffer = make([]byte, 0, bytesToAdd)
	}

	b.Buffer = append(b.Buffer, chunk[:bytesToAdd]...)
}

// Reset clears the buffer for reuse
func (b *bodyBuffer) Reset(phase BodyBufferPhase) {
	b.Buffer = nil
	b.Truncated = false
	b.Phase = phase
}
