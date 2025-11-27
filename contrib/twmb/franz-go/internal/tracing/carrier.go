package tracing

import (
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

// TODO: Should we rename this? It carries the record plus headers
// kafkaHeadersCarrier implements tracer.TextMapWriter for Kafka headers
type kafkaHeadersCarrier struct {
	record Record
}

// compile time type assertion
var _ interface {
	tracer.TextMapWriter
	tracer.TextMapReader
} = (*kafkaHeadersCarrier)(nil)

func NewKafkaHeadersCarrier(r Record) *kafkaHeadersCarrier {
	return &kafkaHeadersCarrier{record: r}
}

// ForeachKey implements tracer.TextMapReader
func (c kafkaHeadersCarrier) ForeachKey(handler func(key, val string) error) error {
	for _, h := range c.record.GetHeaders() {
		err := handler(h.GetKey(), string(h.GetValue()))
		if err != nil {
			return err
		}
	}
	return nil
}

// Set implements tracer.TextMapWriter
func (c *kafkaHeadersCarrier) Set(key, val string) {
	headers := c.record.GetHeaders()
	// If header is already set, overwrite it
	for i, h := range headers {
		if h.GetKey() == key {
			headers[i] = KafkaHeader{
				Key:   key,
				Value: []byte(val),
			}
			c.record.SetHeaders(headers)
			return
		}
	}

	// If header is not set, append it
	c.record.SetHeaders(append(headers, KafkaHeader{
		Key:   key,
		Value: []byte(val),
	}))
}

// ExtractSpanContext extracts the SpanContext from a Record
func ExtractSpanContext(r Record) (*tracer.SpanContext, error) {
	return tracer.Extract(NewKafkaHeadersCarrier(r))
}
