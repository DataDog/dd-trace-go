// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadog.com/).
// Copyright 2018 Datadog, Inc.

package tracer

import (
	"bytes"
	"encoding/binary"
	"fmt"

	"github.com/tinylib/msgp/msgp"
)

// maxLength indicates the maximum number of items supported in a msgpack-encoded array.
// See: https://github.com/msgpack/msgpack/blob/master/spec.md#array-format-family
const maxLength = 1<<32 - 1

// errOverflow is returned when maxLength is exceeded.
var errOverflow = fmt.Errorf("maximum msgpack array length (%d) exceeded", maxLength)

// packedSpans represents a slice of spans encoded in msgpack format. It allows adding spans
// sequentially while keeping track of their count.
type packedSpans struct {
	count uint64       // number of items in slice
	buf   bytes.Buffer // msgpack encoded items (without header)
}

// add adds the given span to the trace.
func (s *packedSpans) add(span *span) error {
	if s.count >= maxLength {
		return errOverflow
	}
	if err := msgp.Encode(&s.buf, span); err != nil {
		return err
	}
	s.count++
	return nil
}

// size returns the number of bytes that would be returned by a call to bytes().
func (s *packedSpans) size() int {
	return s.buf.Len() + arrayHeaderSize(s.count)
}

// reset resets the packedSpans.
func (s *packedSpans) reset() {
	s.count = 0
	s.buf.Reset()
}

// bytes returns the msgpack encoded set of bytes that represents the entire slice.
func (s *packedSpans) buffer() *bytes.Buffer {
	var header [8]byte
	off := arrayHeader(&header, s.count)
	var buf bytes.Buffer
	buf.Write(header[off:])
	buf.Write(s.buf.Bytes())
	return &buf
}

// arrayHeader writes the msgpack array header for a slice of length n into out.
// It returns the offset at which to begin reading from out. For more information,
// see the msgpack spec:
// https://github.com/msgpack/msgpack/blob/master/spec.md#array-format-family
func arrayHeader(out *[8]byte, n uint64) (off int) {
	const (
		msgpackArrayFix byte = 144  // up to 15 items
		msgpackArray16       = 0xdc // up to 2^16-1 items, followed by size in 2 bytes
		msgpackArray32       = 0xdd // up to 2^32-1 items, followed by size in 4 bytes
	)
	off = 8 - arrayHeaderSize(n)
	switch {
	case n <= 15:
		out[off] = msgpackArrayFix + byte(n)
	case n <= 1<<16-1:
		binary.BigEndian.PutUint64(out[:], n) // writes 2 bytes
		out[off] = msgpackArray16
	case n <= 1<<32-1:
		fallthrough
	default:
		binary.BigEndian.PutUint64(out[:], n) // writes 4 bytes
		out[off] = msgpackArray32
	}
	return off
}

// arrayHeaderSize returns the size in bytes of a header for a msgpack array of length n.
func arrayHeaderSize(n uint64) int {
	switch {
	case n == 0:
		return 0
	case n <= 15:
		return 1
	case n <= 1<<16-1:
		return 3
	case n <= 1<<32-1:
		fallthrough
	default:
		return 5
	}
}
