package tracing

import (
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"

	"github.com/twmb/franz-go/pkg/kgo"
)

// TODO: Should we rename this? It carries the record plus headers
// kafkaHeadersCarrier implements tracer.TextMapWriter for Kafka headers
type kafkaHeadersCarrier struct {
	record *kgo.Record
}

// compile time type assertion
var _ interface {
	tracer.TextMapWriter
	tracer.TextMapReader
} = (*kafkaHeadersCarrier)(nil)

func NewKafkaHeadersCarrier(r *kgo.Record) *kafkaHeadersCarrier {
	return &kafkaHeadersCarrier{record: r}
}

// NOTE: The way in which the propagator reads all the info it needs to read.
// ForeachKey conforms to the TextMapReader interface.
// https://github.com/DataDog/dd-trace-go/blob/45246a0188c9c1cd73db516229ce1e6f19c1ecac/ddtrace/tracer/textmap.go#L694
func (c kafkaHeadersCarrier) ForeachKey(handler func(key, val string) error) error {
	for _, h := range c.record.Headers {
		err := handler(h.Key, string(h.Value))
		if err != nil {
			return err
		}
	}
	return nil
}

// Set implements tracer.TextMapWriter - adds trace propagation headers
func (c *kafkaHeadersCarrier) Set(key, val string) {
	// Ensure the header is not already set, if it is, overwrite it
	for i := range c.record.Headers {
		if c.record.Headers[i].Key == key {
			c.record.Headers[i] = kgo.RecordHeader{
				Key:   key,
				Value: []byte(val),
			}
			return
		}
	}

	c.record.Headers = append(c.record.Headers, kgo.RecordHeader{
		Key:   key,
		Value: []byte(val),
	})
}

// ExtractSpanContext retrieves the SpanContext from a kgo.Record.
func ExtractSpanContext(r *kgo.Record) (*tracer.SpanContext, error) {
	return tracer.Extract(NewKafkaHeadersCarrier(r))
}
