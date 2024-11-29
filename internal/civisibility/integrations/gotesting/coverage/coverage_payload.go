// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package coverage

import (
	"bytes"
	"encoding/binary"
	"io"
	"sync/atomic"
	"time"

	"github.com/tinylib/msgp/msgp"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/utils/telemetry"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

// coveragePayload is a slim copy of the payload struct from the tracer package.
type coveragePayload struct {
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

	// serializationTime time to do serialization
	serializationTime time.Duration
}

var _ io.Reader = (*coveragePayload)(nil)

// newCoveragePayload returns a ready to use coverage payload.
func newCoveragePayload() *coveragePayload {
	p := &coveragePayload{
		header: make([]byte, 8),
		off:    8,
	}
	return p
}

// push pushes a new item into the stream.
func (p *coveragePayload) push(testCoverageData *ciTestCoverageData) error {
	p.buf.Grow(testCoverageData.Msgsize())
	startTime := time.Now()
	defer func() {
		p.serializationTime += time.Since(startTime)
	}()
	if err := msgp.Encode(&p.buf, testCoverageData); err != nil {
		return err
	}
	atomic.AddUint32(&p.count, 1)
	p.updateHeader()
	return nil
}

// itemCount returns the number of items available in the srteam.
func (p *coveragePayload) itemCount() int {
	return int(atomic.LoadUint32(&p.count))
}

// size returns the payload size in bytes. After the first read the value becomes
// inaccurate by up to 8 bytes.
func (p *coveragePayload) size() int {
	return p.buf.Len() + len(p.header) - p.off
}

// reset sets up the payload to be read a second time. It maintains the
// underlying byte contents of the buffer. reset should not be used in order to
// reuse the payload for another set of traces.
func (p *coveragePayload) reset() {
	p.updateHeader()
	if p.reader != nil {
		p.reader.Seek(0, 0)
	}
}

// clear empties the payload buffers.
func (p *coveragePayload) clear() {
	p.buf = bytes.Buffer{}
	p.reader = nil
}

// https://github.com/msgpack/msgpack/blob/master/spec.md#array-format-family
const (
	msgpackArrayFix byte = 144  // up to 15 items
	msgpackArray16       = 0xdc // up to 2^16-1 items, followed by size in 2 bytes
	msgpackArray32       = 0xdd // up to 2^32-1 items, followed by size in 4 bytes
)

// updateHeader updates the payload header based on the number of items currently
// present in the stream.
func (p *coveragePayload) updateHeader() {
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
func (p *coveragePayload) Close() error {
	return nil
}

// Read implements io.Reader. It reads from the msgpack-encoded stream.
func (p *coveragePayload) Read(b []byte) (n int, err error) {
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

// getBuffer retrieves the complete body of the CI Visibility coverage payload, including the header.
// It reads the current payload buffer, adds the header, and encodes the entire payload in MessagePack format.
//
// Returns:
//
//	A pointer to a bytes.Buffer containing the encoded CI Visibility coverage payload.
//	An error if reading from the buffer or encoding the payload fails.
func (p *coveragePayload) getBuffer() (*bytes.Buffer, error) {
	startTime := time.Now()
	log.Debug("coveragePayload: .getBuffer (count: %v)", p.itemCount())

	// Create a buffer to read the current payload
	payloadBuf := new(bytes.Buffer)
	if _, err := payloadBuf.ReadFrom(p); err != nil {
		return nil, err
	}

	// Create the final coverage payload
	finalPayload := &ciTestCovPayload{
		Version:   2,
		Coverages: payloadBuf.Bytes(),
	}

	// Create a new buffer to encode the coverage payload in MessagePack format
	encodedBuf := new(bytes.Buffer)
	if err := msgp.Encode(encodedBuf, finalPayload); err != nil {
		return nil, err
	}

	telemetry.EndpointPayloadBytes(telemetry.CodeCoverageEndpointType, float64(encodedBuf.Len()))
	telemetry.EndpointPayloadEventsCount(telemetry.CodeCoverageEndpointType, float64(p.itemCount()))
	telemetry.EndpointEventsSerializationMs(telemetry.CodeCoverageEndpointType, float64((p.serializationTime + time.Since(startTime)).Milliseconds()))
	return encodedBuf, nil
}
