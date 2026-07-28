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
// payloadV04 implements unsafePayload and can be used with the decoder directly. To create
// a new payload use the newPayloadV04 method.
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
	count uint32 // +checkatomic

	// buf holds the sequence of msgpack-encoded items.
	buf bytes.Buffer

	// reader is used for reading the contents of buf.
	reader *bytes.Reader

	// sizeHint is a hint for how large buf should be to avoid slice growth
	// overhead in a steady state.
	sizeHint int
}

var _ io.Reader = (*payloadV04)(nil)

// newPayloadV04 returns a ready to use payload.
func newPayloadV04() *payloadV04 {
	p := &payloadV04{
		header: make([]byte, 8),
		off:    8,
	}
	return p
}

// pushSizeHintPerSpan is a rough per-span byte estimate used to pre-grow buf
// instead of computing t.Msgsize() (an exact but comparatively expensive walk
// of every span's meta/metrics/spanLinks/spanEvents). Benchmarked against the
// exact walk across simple/spankind/detailed span shapes at 1-1000 spans:
// consistently faster (roughly 7-25%, larger wins on tag-heavier spans, see
// BenchmarkPayloadVersions), with allocation *count* unchanged either way --
// the win is CPU avoided, not allocations avoided.
//
// Note that t.Msgsize() is itself a conservative *upper bound*, not the real
// encoded size: msgpack's variable-length integer encoding means Msgsize()'s
// generated code assumes worst-case fixed-width ints, so it overestimates
// real span size roughly 2x in this repo's test fixtures (measured directly:
// "simple" spans encode to ~127 B/span, "detailed" -- spanLinks+spanEvents+1
// tag -- to ~275 B/span, 4-tag "low cardinality" spans to ~311 B/span, all
// well under what Msgsize() reports for the same spans). So 300 isn't
// threading a needle between "accurate for heavy spans" and "wasteful for
// light spans" -- larger constants (tried 450/500/600) already exceed every
// real per-span size measured here and just add more waste with no
// corresponding benefit, which is why they benchmarked worse across the
// board rather than better for tag-heavy spans specifically. 300 mirrors the
// constant payloadV1 already uses for the same purpose (payload_v1.go).
// Under-estimating here is harmless: bytes.Buffer grows itself if exceeded,
// and it doesn't feed the payloadSizeLimit flush check in writer.go, which
// reads the buffer's actual post-encode length instead.
const pushSizeHintPerSpan = 300

// push pushes a new item into the stream.
func (p *payloadV04) push(t spanList) (stats payloadStats, err error) {
	// sizeHint is only honored on the first push of a cycle; grow() defers the
	// actual allocation until here so an idle payload never pins a buffer.
	growTo := max(len(t)*pushSizeHintPerSpan, p.sizeHint)
	p.sizeHint = 0
	p.buf.Grow(growTo)
	if err := msgp.Encode(&p.buf, t); err != nil {
		return payloadStats{}, err
	}
	p.recordItem()
	return p.stats(), nil
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
	atomic.StoreUint32(&p.count, 0)
	p.off = 8
	p.sizeHint = 0
}

// grow ensures the buffer can accommodate n more bytes. Before the first push
// of a cycle it defers to a size hint instead of allocating immediately, so an
// idle payload never pins a buffer; ciVisibilityPayload calls this on every
// push, after which it falls through to an immediate grow.
func (p *payloadV04) grow(n int) {
	if p.itemCount() == 0 {
		p.sizeHint = n
		return
	}
	p.buf.Grow(n)
}

// recordItem records that an item was added and updates the header.
func (p *payloadV04) recordItem() {
	atomic.AddUint32(&p.count, 1)
	p.updateHeader()
}

// stats returns the current stats of the payload.
func (p *payloadV04) stats() payloadStats {
	return payloadStats{
		size:      p.size(),
		itemCount: int(atomic.LoadUint32(&p.count)),
	}
}

// protocol returns the protocol version of the payload.
func (p *payloadV04) protocol() float64 {
	return traceProtocolV04
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

// Write implements io.Writer. It writes data directly to the buffer.
func (p *payloadV04) Write(data []byte) (n int, err error) {
	return p.buf.Write(data)
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
