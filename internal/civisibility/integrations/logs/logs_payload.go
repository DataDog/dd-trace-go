// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package logs

import (
	"bytes"
	"encoding/json"
	"io"
	"sync"
	"time"
)

// logsPayload is a slim copy of the logs payload struct.
type logsPayload struct {
	// count specifies the number of items in the stream.
	count int

	// buf holds the sequence of json-encoded items.
	buf *bytes.Buffer

	// reader is used for reading the contents of buf.
	reader *bytes.Reader

	// serializationTime time to do serialization
	serializationTime time.Duration

	// mu is a mutex to protect concurrent access to the payload.
	mu sync.RWMutex
}

var _ io.Reader = (*logsPayload)(nil)

// newLogsPayload returns a ready to use logs payload.
func newLogsPayload() *logsPayload {
	return &logsPayload{
		buf: bytes.NewBuffer([]byte{byte('[')}),
	}
}

// push pushes a new item into the stream.
func (p *logsPayload) push(logEntryData *logEntry) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.reader != nil {
		// If the reader is already set, we cannot push new items.
		return io.ErrClosedPipe
	}

	startTime := time.Now()
	defer func() {
		p.serializationTime += time.Since(startTime)
	}()

	var val []byte
	var err error
	if val, err = json.Marshal(logEntryData); err != nil {
		return err
	}

	p.count = p.count + 1 // increment the count after acquiring the lock to ensure consistency
	if p.count > 1 {
		p.buf.WriteByte(',')
	}
	p.buf.Write(val)
	return nil
}

// itemCount returns the number of items available in the srteam.
func (p *logsPayload) itemCount() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.count
}

// size returns the payload size in bytes. After the first read the value becomes
// inaccurate by up to 8 bytes.
func (p *logsPayload) size() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.reader != nil {
		return p.buf.Len() // the reader is already set, so the array is closed
	}
	return p.buf.Len() + 1 // 1 bytes for the array closing bracket ']'
}

// reset sets up the payload to be read a second time. It maintains the
// underlying byte contents of the buffer. reset should not be used in order to
// reuse the payload for another set of traces.
func (p *logsPayload) reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.reader != nil {
		p.reader.Seek(0, 0)
	}
}

// clear empties the payload buffers.
func (p *logsPayload) clear() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.buf = bytes.NewBuffer([]byte{byte('[')})
	p.reader = nil
	p.count = 0
}

// Close implements io.Closer
func (p *logsPayload) Close() error {
	return nil
}

// Read implements io.Reader. It reads from the msgpack-encoded stream.
func (p *logsPayload) Read(b []byte) (n int, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.reader == nil {
		p.buf.WriteByte(']') // close the array
		p.reader = bytes.NewReader(p.buf.Bytes())
	}
	return p.reader.Read(b)
}
