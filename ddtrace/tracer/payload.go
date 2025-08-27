// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"bytes"
	"encoding/binary"
	"io"
	"sync"
	"sync/atomic"

	"github.com/tinylib/msgp/msgp"
)

// payloadStats contains the statistics of a payload.
type payloadStats struct {
	size      int // size in bytes
	itemCount int // number of items (traces)
}

// payloadWriter defines the interface for writing data to a payload.
type payloadWriter interface {
	io.Writer

	push(t spanList) (stats payloadStats, err error)
	grow(n int)
	reset()
	clear()

	// recordItem records that an item was added and updates the header
	recordItem()
}

// payloadReader defines the interface for reading data from a payload.
type payloadReader interface {
	io.Reader
	io.Closer

	stats() payloadStats
	size() int
	itemCount() int
	protocol() float64
}

// payload combines both reading and writing operations for a payload.
type payload interface {
	payloadWriter
	payloadReader
}

// unsafePayload is a wrapper on top of the msgpack encoder which allows constructing an
// encoded array by pushing its entries sequentially, one at a time. It basically
// allows us to encode as we would with a stream, except that the contents of the stream
// can be read as a slice by the msgpack decoder at any time. It follows the guidelines
// from the msgpack array spec:
// https://github.com/msgpack/msgpack/blob/master/spec.md#array-format-family
//
// unsafePayload implements io.Reader and can be used with the decoder directly.
//
// unsafePayload is not safe for concurrent use.
//
// unsafePayload is meant to be used only once and eventually dismissed with the
// single exception of retrying failed flush attempts.
//
// ⚠️  Warning!
//
// The payload should not be reused for multiple sets of traces. Resetting the
// payload for re-use requires the transport to wait for the HTTP package to
// Close the request body before attempting to re-use it again! This requires
// additional logic to be in place. See:
//
// • https://github.com/golang/go/blob/go1.16/src/net/http/client.go#L136-L138
// • https://github.com/DataDog/dd-trace-go/pull/475
// • https://github.com/DataDog/dd-trace-go/pull/549
// • https://github.com/DataDog/dd-trace-go/pull/976
type unsafePayload struct {
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

	// protocolVersion specifies the trace protocolVersion to use.
	protocolVersion float64
}

var _ io.Reader = (*unsafePayload)(nil)

// newUnsafePayload returns a ready to use unsafe payload.
func newUnsafePayload(protocol float64) *unsafePayload {
	p := &unsafePayload{
		header:          make([]byte, 8),
		off:             8,
		protocolVersion: protocol,
	}
	return p
}

// push pushes a new item into the stream.
func (p *unsafePayload) push(t []*Span) (stats payloadStats, err error) {
	sl := spanList(t)
	p.buf.Grow(sl.Msgsize())
	if err := msgp.Encode(&p.buf, sl); err != nil {
		return payloadStats{}, err
	}
	p.recordItem()
	return p.stats(), nil
}

// itemCount returns the number of items available in the stream.
func (p *unsafePayload) itemCount() int {
	return int(atomic.LoadUint32(&p.count))
}

// size returns the payload size in bytes. After the first read the value becomes
// inaccurate by up to 8 bytes.
func (p *unsafePayload) size() int {
	return p.buf.Len() + len(p.header) - p.off
}

// reset sets up the payload to be read a second time. It maintains the
// underlying byte contents of the buffer. reset should not be used in order to
// reuse the payload for another set of traces.
func (p *unsafePayload) reset() {
	p.updateHeader()
	if p.reader != nil {
		p.reader.Seek(0, 0)
	}
}

// clear empties the payload buffers.
func (p *unsafePayload) clear() {
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
func (p *unsafePayload) updateHeader() {
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
func (p *unsafePayload) Close() error {
	return nil
}

// Read implements io.Reader. It reads from the msgpack-encoded stream.
func (p *unsafePayload) Read(b []byte) (n int, err error) {
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

// Write implements io.Writer. It writes data directly to the buffer.
func (p *unsafePayload) Write(data []byte) (n int, err error) {
	return p.buf.Write(data)
}

// grow grows the buffer to ensure it can accommodate n more bytes.
func (p *unsafePayload) grow(n int) {
	p.buf.Grow(n)
}

// recordItem records that an item was added and updates the header.
func (p *unsafePayload) recordItem() {
	atomic.AddUint32(&p.count, 1)
	p.updateHeader()
}

// stats returns the current stats of the payload.
func (p *unsafePayload) stats() payloadStats {
	return payloadStats{
		size:      p.size(),
		itemCount: int(atomic.LoadUint32(&p.count)),
	}
}

// protocol returns the protocol version of the payload.
func (p *unsafePayload) protocol() float64 {
	return p.protocolVersion
}

var _ io.Reader = (*safePayload)(nil)

// newPayload returns a ready to use thread-safe payload.
func newPayload(protocol float64) payload {
	return &safePayload{
		p: newUnsafePayload(protocol),
	}
}

// safePayload provides a thread-safe wrapper around unsafePayload.
type safePayload struct {
	mu sync.RWMutex
	p  *unsafePayload
}

// push pushes a new item into the stream in a thread-safe manner.
func (sp *safePayload) push(t spanList) (stats payloadStats, err error) {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	return sp.p.push(t)
}

// itemCount returns the number of items available in the stream in a thread-safe manner.
func (sp *safePayload) itemCount() int {
	// Use direct atomic access for better performance - no mutex needed
	return int(atomic.LoadUint32(&sp.p.count))
}

// size returns the payload size in bytes in a thread-safe manner.
func (sp *safePayload) size() int {
	sp.mu.RLock()
	defer sp.mu.RUnlock()
	return sp.p.size()
}

// reset sets up the payload to be read a second time in a thread-safe manner.
func (sp *safePayload) reset() {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	sp.p.reset()
}

// clear empties the payload buffers in a thread-safe manner.
func (sp *safePayload) clear() {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	sp.p.clear()
}

// Read implements io.Reader in a thread-safe manner.
func (sp *safePayload) Read(b []byte) (n int, err error) {
	// Note: Read modifies internal state (off, reader), so we need full lock
	sp.mu.Lock()
	defer sp.mu.Unlock()
	return sp.p.Read(b)
}

// Close implements io.Closer in a thread-safe manner.
func (sp *safePayload) Close() error {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	return sp.p.Close()
}

// Write implements io.Writer in a thread-safe manner.
func (sp *safePayload) Write(data []byte) (n int, err error) {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	return sp.p.Write(data)
}

// grow grows the buffer to ensure it can accommodate n more bytes in a thread-safe manner.
func (sp *safePayload) grow(n int) {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	sp.p.grow(n)
}

// recordItem records that an item was added and updates the header in a thread-safe manner.
func (sp *safePayload) recordItem() {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	sp.p.recordItem()
}

// stats returns the current stats of the payload in a thread-safe manner.
func (sp *safePayload) stats() payloadStats {
	sp.mu.RLock()
	defer sp.mu.RUnlock()
	return sp.p.stats()
}

// protocol returns the protocol version of the payload in a thread-safe manner.
func (sp *safePayload) protocol() float64 {
	// Protocol is immutable after creation - no lock needed
	return sp.p.protocol()
}
