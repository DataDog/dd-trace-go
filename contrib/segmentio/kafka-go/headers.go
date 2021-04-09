package kafka

import (
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"

	"github.com/segmentio/kafka-go"
)

// A MessageCarrier injects and extracts traces from a sarama.ProducerMessage.
type MessageCarrier struct {
	msg *kafka.Message
}

var _ interface {
	tracer.TextMapReader
	tracer.TextMapWriter
} = (*MessageCarrier)(nil)

// ForeachKey iterates over every header.
func (c MessageCarrier) ForeachKey(handler func(key, val string) error) error {
	for _, h := range c.msg.Headers {
		err := handler(h.Key, string(h.Value))
		if err != nil {
			return err
		}
	}
	return nil
}

// Set sets a header.
func (c MessageCarrier) Set(key, val string) {
	// ensure uniqueness of keys
	for i := 0; i < len(c.msg.Headers); i++ {
		if string(c.msg.Headers[i].Key) == key {
			c.msg.Headers = append(c.msg.Headers[:i], c.msg.Headers[i+1:]...)
			i--
		}
	}
	c.msg.Headers = append(c.msg.Headers, kafka.Header{
		Key:   key,
		Value: []byte(val),
	})
}

// NewMessageCarrier creates a new MessageCarrier.
func NewMessageCarrier(msg *kafka.Message) MessageCarrier {
	return MessageCarrier{msg}
}
