package tracer

import (
	"bytes"
	"encoding/binary"
	"io"

	"github.com/ugorji/go/codec"
)

// payload is a wrapper on top of the msgpack encoder which allows constructing an
// encoded array by pushing its entries sequentially, one at a time. It basically
// allows us to encode as we would with a stream, except that the contents of the stream
// can be read as a slice by the msgpack decoder at any time. It follows the guidelines
// from the msgpack array spec:
// https://github.com/msgpack/msgpack/blob/master/spec.md#array-format-family
//
// payload implements io.Reader an can be used with the decoder directly. To create
// a new payload use the newPayload method.
//
// payload is not safe for concurrent use.
//
// Example:
//
//   p := newPayload()
//   // add some items
//   p.push(1)
//   p.push(2)
//   p.push(3)
//   // decode into a slice
//   var numbers []int
//   codec.NewDecoder(p, &codec.MsgpackHandler{}).Decode(&numbers)
//   // numbers == []int{1, 2, 3}
type payload struct {
	// header specifies the first few bytes in the msgpack stream
	// indicating the type of array (fixarray, array16 or array32)
	// and the number of items contained in the stream.
	header []byte

	// off specifies how many bytes of the header have been read by
	// the reader.
	off int

	// count specifies the number of items in the stream.
	count uint64

	// buf holds the sequence of msgpack-encoded items.
	buf bytes.Buffer

	// encoder holds a reference to the encoder used to write into buf.
	encoder *codec.Encoder
}

var _ io.Reader = (*payload)(nil)

// newPayload returns a ready to use payload.
func newPayload() *payload {
	var p payload
	p.encoder = codec.NewEncoder(&p.buf, &codec.MsgpackHandle{})
	return &p
}

// push pushes a new item into the stream.
func (p *payload) push(v interface{}) error {
	if err := p.encoder.Encode(v); err != nil {
		return err
	}
	p.count++
	p.updateHeader()
	return nil
}

// itemCount returns the number of items available in the srteam.
func (p *payload) itemCount() int {
	return int(p.count)
}

// size returns the payload size in bytes.
func (p *payload) size() int {
	return p.buf.Len() + len(p.header)
}

// reset resets the internal buffer, counter, read offset and header,
// but keeps the encoder.
func (p *payload) reset() {
	p.header = []byte{}
	p.off = 0
	p.count = 0
	p.buf.Reset()
}

// updateHeader updates the payload header based on the number of items currently
// present in the stream.
func (p *payload) updateHeader() {
	// https://github.com/msgpack/msgpack/blob/master/spec.md#array-format-family
	const (
		msgpackArrayFix byte = 144  // up to 15 items
		msgpackArray16       = 0xdc // up to 2^16-1 items, followed by size in 2 bytes
		msgpackArray32       = 0xdd // up to 2^32-1 items, followed by size in 4 bytes
	)
	n := p.count
	if n <= 15 {
		p.header = []byte{msgpackArrayFix + byte(n)}
		return
	}
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, n)
	if n <= 1<<16-1 {
		p.header = append([]byte{msgpackArray16}, b[len(b)-2:]...)
	} else {
		p.header = append([]byte{msgpackArray32}, b[len(b)-4:]...)
	}
}

// Read implements io.Reader. It reads from the msgpack-encoded stream.
func (p *payload) Read(b []byte) (n int, err error) {
	if p.off < len(p.header) {
		// reading header
		n = copy(b, p.header[p.off:])
		p.off += n
		return n, nil
	}
	return p.buf.Read(b)
}
