// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package logs

import (
	"bytes"
	"encoding/binary"
	"io"
	"sync/atomic"
	"time"

	"github.com/tinylib/msgp/msgp"
)

// logsPayload is a slim copy of the logs payload struct.
type logsPayload struct {
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

var _ io.Reader = (*logsPayload)(nil)

// newLogsPayload returns a ready to use logs payload.
func newLogsPayload() *logsPayload {
	p := &logsPayload{
		header: make([]byte, 8),
		off:    8,
	}
	return p
}

// push pushes a new item into the stream.
func (p *logsPayload) push(logEntryData *logEntry) error {
	p.buf.Grow(logEntryData.Msgsize())
	startTime := time.Now()
	defer func() {
		p.serializationTime += time.Since(startTime)
	}()
	if err := msgp.Encode(&p.buf, logEntryData); err != nil {
		return err
	}
	atomic.AddUint32(&p.count, 1)
	p.updateHeader()
	return nil
}

// itemCount returns the number of items available in the srteam.
func (p *logsPayload) itemCount() int {
	return int(atomic.LoadUint32(&p.count))
}

// size returns the payload size in bytes. After the first read the value becomes
// inaccurate by up to 8 bytes.
func (p *logsPayload) size() int {
	return p.buf.Len() + len(p.header) - p.off
}

// reset sets up the payload to be read a second time. It maintains the
// underlying byte contents of the buffer. reset should not be used in order to
// reuse the payload for another set of traces.
func (p *logsPayload) reset() {
	p.updateHeader()
	if p.reader != nil {
		p.reader.Seek(0, 0)
	}
}

// clear empties the payload buffers.
func (p *logsPayload) clear() {
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
func (p *logsPayload) updateHeader() {
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
func (p *logsPayload) Close() error {
	return nil
}

// Read implements io.Reader. It reads from the msgpack-encoded stream.
func (p *logsPayload) Read(b []byte) (n int, err error) {
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
