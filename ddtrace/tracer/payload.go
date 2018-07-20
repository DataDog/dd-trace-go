package tracer

import (
	"bytes"
	"encoding/binary"
	"io"

	"github.com/tinylib/msgp/msgp"
)

// payload is a wrapper on top of the msgpack encoder which allows constructing an
// encoded array by pushing its entries sequentially, one at a time. It basically
// allows us to encode as we would with a stream, except that the contents of the stream
// can be read as a slice by the msgpack decoder at any time. It follows the guidelines
// from the msgpack array spec:
// https://github.com/msgpack/msgpack/blob/master/spec.md#array-format-family
//
// payload implements io.Reader and can be used with the decoder directly. To create
// a new payload use the newPayload method.
//
// payload is not safe for concurrent use.
//
// This structure basically allows us to push traces into the payload one at a time
// in order to always have knowledge of the payload size, but also making it possible
// for the agent to decode it as an array.
type payload struct {
	// header specifies the first few bytes in the msgpack stream
	// indicating the type of array (fixarray, array16 or array32)
	// and the number of items contained in the stream.
	header []byte

	// off specifies the current read position on the header.
	off int

	// traces holds the sequence of traces to returns.
	traces []spanList

	s int //encoded payload size in bytes

	buf bytes.Buffer
}

var _ io.Reader = (*payload)(nil)

// newPayload returns a ready to use payload.
func newPayload() *payload {
	p := &payload{
		header: make([]byte, 8),
		off:    8,
	}
	return p
}

// push pushes a new item into the stream.
func (p *payload) push(t spanList) error {
	p.traces = append(p.traces, t)
	p.updateHeader()
	p.s += t.Msgsize()
	return nil
}

// itemCount returns the number of items available in the stream.
func (p *payload) itemCount() int {
	return len(p.traces)
}

// size returns the payload size in bytes. After the first read the value becomes
// inaccurate by up to 8 bytes.
func (p *payload) size() int {
	return len(p.header) + p.s
}

// reset resets the internal traces slice, buffer, counter, size tracker and read offset.
func (p *payload) reset() {
	p.off = 8
	for i := range p.traces {
		// dereference all traces in the underlying array to let the GC work its magic
		p.traces[i] = nil
	}
	p.traces = p.traces[:0]
	p.s = 0
	p.buf.Reset()
}

// https://github.com/msgpack/msgpack/blob/master/spec.md#array-format-family
const (
	msgpackArrayFix byte = 144  // up to 15 items
	msgpackArray16       = 0xdc // up to 2^16-1 items, followed by size in 2 bytes
	msgpackArray32       = 0xdd // up to 2^32-1 items, followed by size in 4 bytes
)

// updateHeader updates the payload header based on the number of items currently
// present in the stream.
func (p *payload) updateHeader() {
	n := uint64(len(p.traces))
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

// Read implements io.Reader. It reads from the msgpack-encoded stream.
func (p *payload) Read(b []byte) (n int, err error) {
	if p.off < len(p.header) {
		// reading header
		n, err := p.buf.Write(p.header[p.off:])
		if err != nil {
			return n, err
		}
		p.off += n
	}
	for len(p.traces) != 0 && p.buf.Len() <= len(b) {
		// fill buffer
		msgp.Encode(&p.buf, p.traces[0])
		if err != nil {
			return 0, err
		}
		p.s -= p.traces[0].Msgsize()
		p.traces = p.traces[1:]
	}

	return p.buf.Read(b)
}
