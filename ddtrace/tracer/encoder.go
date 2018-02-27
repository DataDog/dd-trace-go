package tracer

import (
	"bytes"
	"encoding/json"
	"io"

	"github.com/ugorji/go/codec"
)

const (
	jsoncontentType    = "application/json"
	msgpackcontentType = "application/msgpack"
)

// encoder is a generic interface that expects encoding methods for traces and
// services, and a Read() method that will be used by the http handler
type encoder interface {
	io.Reader

	encodeTraces(traces [][]*span) error
	encodeServices(services map[string]service) error
	contentType() string
}

var mh codec.MsgpackHandle

var _ encoder = (*msgpackEncoder)(nil)

// msgpackEncoder encodes a list of traces in Msgpack format
type msgpackEncoder struct {
	buffer         *bytes.Buffer
	encoder        *codec.Encoder
	msgcontentType string
}

func newMsgpackEncoder() *msgpackEncoder {
	buffer := &bytes.Buffer{}
	encoder := codec.NewEncoder(buffer, &mh)

	return &msgpackEncoder{
		buffer:         buffer,
		encoder:        encoder,
		msgcontentType: msgpackcontentType,
	}
}

// encodeTraces serializes the given trace list into the internal buffer,
// returning the error if any.
func (e *msgpackEncoder) encodeTraces(traces [][]*span) error {
	return e.encoder.Encode(traces)
}

// encodeServices serializes a service map into the internal buffer.
func (e *msgpackEncoder) encodeServices(services map[string]service) error {
	return e.encoder.Encode(services)
}

// Read values from the internal buffer
func (e *msgpackEncoder) Read(p []byte) (int, error) {
	return e.buffer.Read(p)
}

// contentType return the msgpackEncoder content-type
func (e *msgpackEncoder) contentType() string {
	return e.msgcontentType
}

var _ encoder = (*jsonEncoder)(nil)

// jsonEncoder encodes a list of traces in JSON format
type jsonEncoder struct {
	buffer         *bytes.Buffer
	encoder        *json.Encoder
	msgContentType string
}

// newJSONEncoder returns a new encoder for the JSON format.
func newJSONEncoder() *jsonEncoder {
	buffer := &bytes.Buffer{}
	encoder := json.NewEncoder(buffer)

	return &jsonEncoder{
		buffer:         buffer,
		encoder:        encoder,
		msgContentType: jsoncontentType,
	}
}

// encodeTraces serializes the given trace list into the internal buffer,
// returning the error if any.
func (e *jsonEncoder) encodeTraces(traces [][]*span) error {
	return e.encoder.Encode(traces)
}

// encodeServices serializes a service map into the internal buffer.
func (e *jsonEncoder) encodeServices(services map[string]service) error {
	return e.encoder.Encode(services)
}

// Read values from the internal buffer
func (e *jsonEncoder) Read(p []byte) (int, error) {
	return e.buffer.Read(p)
}

// contentType return the jsonEncoder content-type
func (e *jsonEncoder) contentType() string {
	return e.msgContentType
}

// encoderFactory will provide a new encoder each time we want to flush traces or services.
type encoderFactory func() encoder

func jsonEncoderFactory() encoder {
	return newJSONEncoder()
}

func msgpackEncoderFactory() encoder {
	return newMsgpackEncoder()
}
