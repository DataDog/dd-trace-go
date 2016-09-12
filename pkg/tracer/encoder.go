package tracer

import (
	"encoding/json"
)

// Encoder defines the interface of a generic Encoder
// that must implement the Encode function. This interface
// can be used to write different encoders for different
// Transport interfaces.
type Encoder interface {
	Encode(spans []*Span) ([]byte, error)
}

// JSONEncoder encodes a list of spans in JSON format.
type JSONEncoder struct{}

// NewJSONEncoder returns a new encoder for the JSON format.
func NewJSONEncoder() *JSONEncoder {
	return &JSONEncoder{}
}

// Encode returns a byte array related to the marshalling
// of a list of spans.
func (e *JSONEncoder) Encode(spans []*Span) ([]byte, error) {
	return json.Marshal(spans)
}
