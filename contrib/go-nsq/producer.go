package nsq

import (
	"time"

	"github.com/nsqio/go-nsq"
)

type Producer struct {
	*nsq.Producer
	*traceHelper
}

func NewProducer(addr string, config *Config) (*Producer, error) {
	producer, err := nsq.NewProducer(addr, config.Config)
	if err != nil {
		return nil, err
	}

	return &Producer{
		Producer:    producer,
		traceHelper: newTraceHelper(config),
	}, nil
}

func (this *Producer) Ping() error {
	start := time.Now()
	err := this.Producer.Ping()
	this.traceHelper.trace(start, spanTypeProducer, "Ping", err)

	return err
}

func (this *Producer) Publish(topic string, body []byte) error {
	start := time.Now()
	err := this.Producer.Publish(topic, body)
	this.traceHelper.trace(start, spanTypeProducer, "Publish", err)

	return err
}

func (this *Producer) MultiPublish(topic string, body [][]byte) error {
	start := time.Now()
	err := this.Producer.MultiPublish(topic, body)
	this.traceHelper.trace(start, spanTypeProducer, "MultiPublish", err)

	return err
}

func (this *Producer) PublishAsync(topic string, body []byte, doneChan chan *nsq.ProducerTransaction, args ...interface{}) error {
	start := time.Now()
	err := this.Producer.PublishAsync(topic, body, doneChan, args...)
	this.traceHelper.trace(start, spanTypeProducer, "PublishAsync", err)

	return err
}

func (this *Producer) MultiPublishAsync(topic string, body [][]byte, doneChan chan *nsq.ProducerTransaction, args ...interface{}) error {
	start := time.Now()
	err := this.Producer.MultiPublishAsync(topic, body, doneChan, args...)
	this.traceHelper.trace(start, spanTypeProducer, "MultiPublishAsync", err)

	return err
}

func (this *Producer) DeferredPublish(topic string, delay time.Duration, body []byte) error {
	start := time.Now()
	err := this.Producer.DeferredPublish(topic, delay, body)
	this.traceHelper.trace(start, spanTypeProducer, "DeferredPublish", err)

	return err
}

func (this *Producer) DeferredPublishAsync(topic string, delay time.Duration, body []byte, doneChan chan *nsq.ProducerTransaction, args ...interface{}) error {
	start := time.Now()
	err := this.Producer.DeferredPublishAsync(topic, delay, body, doneChan, args...)
	this.traceHelper.trace(start, spanTypeProducer, "DeferredPublishAsync", err)

	return err
}

func (this *Producer) Stop() {
	start := time.Now()
	this.Producer.Stop()
	this.traceHelper.trace(start, spanTypeProducer, "Stop", nil)
}
