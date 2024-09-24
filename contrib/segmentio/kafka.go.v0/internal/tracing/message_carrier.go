package tracing

import (
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

// A MessageCarrier implements TextMapReader/TextMapWriter for extracting/injecting traces on a kafka.Message
type MessageCarrier struct {
	Message *KafkaMessage
}

var _ interface {
	tracer.TextMapReader
	tracer.TextMapWriter
} = (*MessageCarrier)(nil)

// ForeachKey conforms to the TextMapReader interface.
func (c MessageCarrier) ForeachKey(handler func(key, val string) error) error {
	for _, h := range c.Message.Headers {
		err := handler(h.Key, string(h.Value))
		if err != nil {
			return err
		}
	}
	return nil
}

// Set implements TextMapWriter
func (c MessageCarrier) Set(key, val string) {
	// ensure uniqueness of keys
	for i := 0; i < len(c.Message.Headers); i++ {
		if c.Message.Headers[i].Key == key {
			c.Message.Headers = append(c.Message.Headers[:i], c.Message.Headers[i+1:]...)
			i--
		}
	}
	c.Message.Headers = append(c.Message.Headers, KafkaHeader{
		Key:   key,
		Value: []byte(val),
	})
	c.Message.SetHeaders(c.Message.Headers)
}
