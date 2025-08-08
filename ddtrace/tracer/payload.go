// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"bytes"
	"encoding/binary"
	"io"
	"sync/atomic"

	"github.com/tinylib/msgp/msgp"
)

var _ io.Reader = (*payload_V04)(nil)

// newPayload returns a ready to use payload.
func newPayload() *payload_V04 {
	p := &payload_V04{
		header: make([]byte, 8),
		off:    8,
	}
	return p
}

// push pushes a new item into the stream.
func (p *payload_V04) push(t []*Span) error {
	sl := spanList(t)
	p.buf.Grow(sl.Msgsize())
	if err := msgp.Encode(&p.buf, sl); err != nil {
		return err
	}
	atomic.AddUint32(&p.count, 1)
	p.updateHeader()
	return nil
}

// itemCount returns the number of items available in the stream.
func (p *payload_V04) itemCount() int {
	return int(atomic.LoadUint32(&p.count))
}

// size returns the payload size in bytes. After the first read the value becomes
// inaccurate by up to 8 bytes.
func (p *payload_V04) size() int {
	return p.buf.Len() + len(p.header) - p.off
}

// reset sets up the payload to be read a second time. It maintains the
// underlying byte contents of the buffer. reset should not be used in order to
// reuse the payload for another set of traces.
func (p *payload_V04) reset() {
	p.updateHeader()
	if p.reader != nil {
		p.reader.Seek(0, 0)
	}
}

// clear empties the payload buffers.
func (p *payload_V04) clear() {
	p.buf = bytes.Buffer{}
	p.reader = nil
}

// https://github.com/msgpack/msgpack/blob/master/spec.md#array-format-family
const (
	msgpackArrayFix byte = 144  // up to 15 items
	msgpackArray16  byte = 0xdc // up to 2^16-1 items, followed by size in 2 bytes
	msgpackArray32  byte = 0xdd // up to 2^32-1 items, followed by size in 4 bytes
)

// updateHeader updates the payload header based on the number of items currently
// present in the stream.
func (p *payload_V04) updateHeader() {
	n := uint64(atomic.LoadUint32(&p.count))
	switch {
	case n <= 15:
		p.header[7] = msgpackArrayFix + byte(n)
		p.off = 7
	case n <= 1<<16-1:
		binary.BigEndian.PutUint64(p.header, n) // writes 2 bytes
		p.header[5] = msgpackArray16
		p.off = 5
	default: // n <= 1<<32-1
		binary.BigEndian.PutUint64(p.header, n) // writes 4 bytes
		p.header[3] = msgpackArray32
		p.off = 3
	}
}

// Close implements io.Closer
func (p *payload_V04) Close() error {
	return nil
}

// Read implements io.Reader. It reads from the msgpack-encoded stream.
func (p *payload_V04) Read(b []byte) (n int, err error) {
	if p.off < len(p.header) {
		// reading header
		n = copy(b, p.header[p.off:])
		p.off += n
		return n, nil
	}
	if p.reader == nil {
		p.reader = bytes.NewReader(p.buf.Bytes())
	}
	return p.reader.Read(b)
}
