package nsq

import (
	"time"

	"github.com/nsqio/go-nsq"
)

type Producer struct {
	*nsq.Producer
	*traceHelper
}

func NewProducer(addr string, config *nsq.Config, opts ...Option) (*Producer, error) {
	producer, err := nsq.NewProducer(addr, config)
	if err != nil {
		return nil, err
	}

	cfg := NewConfig(opts...)
	cfg.Config = config

	return &Producer{
		Producer:    producer,
		traceHelper: newTraceHelper(cfg),
	}, nil
}

func (prod *Producer) Ping() error {
	start := time.Now()
	err := prod.Producer.Ping()
	prod.traceHelper.trace(start, spanTypeProducer, "Ping", err)

	return err
}

func (prod *Producer) Publish(topic string, body []byte) error {
	start := time.Now()
	err := prod.Producer.Publish(topic, body)
	prod.traceHelper.trace(start, spanTypeProducer, "Publish", err)

	return err
}

func (prod *Producer) MultiPublish(topic string, body [][]byte) error {
	start := time.Now()
	err := prod.Producer.MultiPublish(topic, body)
	prod.traceHelper.trace(start, spanTypeProducer, "MultiPublish", err)

	return err
}

func (prod *Producer) PublishAsync(topic string, body []byte, doneChan chan *nsq.ProducerTransaction, args ...interface{}) error {
	start := time.Now()
	err := prod.Producer.PublishAsync(topic, body, doneChan, args...)
	prod.traceHelper.trace(start, spanTypeProducer, "PublishAsync", err)

	return err
}

func (prod *Producer) MultiPublishAsync(topic string, body [][]byte, doneChan chan *nsq.ProducerTransaction, args ...interface{}) error {
	start := time.Now()
	err := prod.Producer.MultiPublishAsync(topic, body, doneChan, args...)
	prod.traceHelper.trace(start, spanTypeProducer, "MultiPublishAsync", err)

	return err
}

func (prod *Producer) DeferredPublish(topic string, delay time.Duration, body []byte) error {
	start := time.Now()
	err := prod.Producer.DeferredPublish(topic, delay, body)
	prod.traceHelper.trace(start, spanTypeProducer, "DeferredPublish", err)

	return err
}

func (prod *Producer) DeferredPublishAsync(topic string, delay time.Duration, body []byte, doneChan chan *nsq.ProducerTransaction, args ...interface{}) error {
	start := time.Now()
	err := prod.Producer.DeferredPublishAsync(topic, delay, body, doneChan, args...)
	prod.traceHelper.trace(start, spanTypeProducer, "DeferredPublishAsync", err)

	return err
}

func (prod *Producer) Stop() {
	start := time.Now()
	prod.Producer.Stop()
	prod.traceHelper.trace(start, spanTypeProducer, "Stop", nil)
}
