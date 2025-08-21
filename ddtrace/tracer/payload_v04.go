// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package tracer

import (
	"bytes"
	"encoding/binary"
	"io"
	"sync/atomic"

	"github.com/tinylib/msgp/msgp"
)

// payloadV04 is a wrapper on top of the msgpack encoder which allows constructing an
// encoded array by pushing its entries sequentially, one at a time. It basically
// allows us to encode as we would with a stream, except that the contents of the stream
// can be read as a slice by the msgpack decoder at any time. It follows the guidelines
// from the msgpack array spec:
// https://github.com/msgpack/msgpack/blob/master/spec.md#array-format-family
//
// payloadV04 implements io.Reader and can be used with the decoder directly. To create
// a new payload use the newPayload method.
//
// payloadV04 is not safe for concurrent use.
//
// payloadV04 is meant to be used only once and eventually dismissed with the
// single exception of retrying failed flush attempts.
//
// ⚠️  Warning!
//
// The payloadV04 should not be reused for multiple sets of traces.  Resetting the
// payloadV04 for re-use requires the transport to wait for the HTTP package to
// Close the request body before attempting to re-use it again! This requires
// additional logic to be in place. See:
//
// • https://github.com/golang/go/blob/go1.16/src/net/http/client.go#L136-L138
// • https://github.com/DataDog/dd-trace-go/pull/475
// • https://github.com/DataDog/dd-trace-go/pull/549
// • https://github.com/DataDog/dd-trace-go/pull/976
type payloadV04 struct {
	// header specifies the first few bytes in the msgpack stream
	// indicating the type of array (fixarray, array16 or array32)
	// and the number of items contained in the stream.
	header []byte

	// off specifies the current read position on the header.
	off int

	// count specifies the number of items in the stream.
	count uint32

	// buf holds the sequence of msgpack-encoded items.
	buf bytes.Buffer

	// reader is used for reading the contents of buf.
	reader *bytes.Reader

	// protocol specifies the trace protocol to use.
	protocol float64
}

var _ io.Reader = (*payloadV04)(nil)

// newPayload returns a ready to use payload.
func newPayload() *payloadV04 {
	p := &payloadV04{
		header: make([]byte, 8),
		off:    8,
	}
	return p
}

// push pushes a new item into the stream.
func (p *payloadV04) push(t []*Span) error {
	// if p.protocol == traceProtocolV1 {
	//     // TODO: implement v1.0 encoding
	// } else {
	sl := spanList(t)
	// }
	p.buf.Grow(sl.Msgsize())
	if err := msgp.Encode(&p.buf, sl); err != nil {
		return err
	}
	atomic.AddUint32(&p.count, 1)
	p.updateHeader()
	return nil
}

// itemCount returns the number of items available in the stream.
func (p *payloadV04) itemCount() int {
	return int(atomic.LoadUint32(&p.count))
}

// size returns the payload size in bytes. After the first read the value becomes
// inaccurate by up to 8 bytes.
func (p *payloadV04) size() int {
	return p.buf.Len() + len(p.header) - p.off
}

// reset sets up the payload to be read a second time. It maintains the
// underlying byte contents of the buffer. reset should not be used in order to
// reuse the payload for another set of traces.
func (p *payloadV04) reset() {
	p.updateHeader()
	if p.reader != nil {
		p.reader.Seek(0, 0)
	}
}

// clear empties the payload buffers.
func (p *payloadV04) clear() {
	p.buf = bytes.Buffer{}
	p.reader = nil
}

// updateHeader updates the payload header based on the number of items currently
// present in the stream.
func (p *payloadV04) updateHeader() {
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
func (p *payloadV04) Close() error {
	return nil
}

// Read implements io.Reader. It reads from the msgpack-encoded stream.
func (p *payloadV04) Read(b []byte) (n int, err error) {
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
