package nsq

import (
	"time"

	"github.com/nsqio/go-nsq"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

type Consumer struct {
	*nsq.Consumer
	*traceHelper
}

func NewConsumer(topic string, channel string, config *nsq.Config, opts ...Option) (*Consumer, error) {
	consumer, err := nsq.NewConsumer(topic, channel, config)
	if err != nil {
		return nil, err
	}

	cfg := NewConfig(opts...)
	cfg.Config = config

	return &Consumer{
		Consumer:    consumer,
		traceHelper: newTraceHelper(cfg),
	}, nil
}

func (cons *Consumer) Stats() *nsq.ConsumerStats {
	start := time.Now()
	stats := cons.Consumer.Stats()
	cons.traceHelper.trace(start, spanTypeConsumer, "Stats", nil)

	return stats
}

func (cons *Consumer) SetBehaviorDelegate(cb interface{}) {
	start := time.Now()
	cons.Consumer.SetBehaviorDelegate(cb)
	cons.traceHelper.trace(start, spanTypeConsumer, "SetBehaviorDelegate", nil)
}

func (cons *Consumer) IsStarved() bool {
	start := time.Now()
	is := cons.Consumer.IsStarved()
	cons.traceHelper.trace(start, spanTypeConsumer, "IsStarved", nil)

	return is
}

func (cons *Consumer) ChangeMaxInFlight(maxInFlight int) {
	start := time.Now()
	cons.Consumer.ChangeMaxInFlight(maxInFlight)
	cons.traceHelper.trace(start, spanTypeConsumer, "ChangeMaxInFlight", nil)
}

func (cons *Consumer) ConnectToNSQLookupd(addr string) error {
	start := time.Now()
	err := cons.Consumer.ConnectToNSQLookupd(addr)
	cons.traceHelper.trace(start, spanTypeConsumer, "ConnectToNSQLookupd", err)

	return err
}

func (cons *Consumer) ConnectToNSQLookupds(addresses []string) error {
	start := time.Now()
	err := cons.Consumer.ConnectToNSQLookupds(addresses)
	cons.traceHelper.trace(start, spanTypeConsumer, "ConnectToNSQLookupds", err)

	return err
}

func (cons *Consumer) ConnectToNSQDs(addresses []string) error {
	start := time.Now()
	err := cons.Consumer.ConnectToNSQDs(addresses)
	cons.traceHelper.trace(start, spanTypeConsumer, "ConnectToNSQDs", err)

	return err
}

func (cons *Consumer) ConnectToNSQD(addr string) error {
	start := time.Now()
	err := cons.Consumer.ConnectToNSQD(addr)
	cons.traceHelper.trace(start, spanTypeConsumer, "ConnectToNSQD", err)

	return err
}

func (cons *Consumer) DisconnectFromNSQD(addr string) error {
	start := time.Now()
	err := cons.Consumer.DisconnectFromNSQD(addr)
	cons.traceHelper.trace(start, spanTypeConsumer, "DisconnectFromNSQD", err)

	return err
}

func (cons *Consumer) DisconnectFromNSQLookupd(addr string) error {
	start := time.Now()
	err := cons.Consumer.DisconnectFromNSQLookupd(addr)
	cons.traceHelper.trace(start, spanTypeConsumer, "DisconnectFromNSQLookupd", err)

	return err
}

func (cons *Consumer) AddHandler(handler nsq.Handler) {
	start := time.Now()
	cons.Consumer.AddHandler(func(next nsq.Handler) nsq.Handler {
		return nsq.HandlerFunc(func(message *nsq.Message) error {
			opts := []ddtrace.StartSpanOption{
				tracer.ServiceName(cons.cfg.service),
				tracer.ResourceName("nsq.Consumer.MessageHandler"),
				tracer.SpanType(string(spanTypeProducer)),
			}

			span, ctx := tracer.StartSpanFromContext(cons.cfg.ctx, "Consumer.HandleMessage", opts...)
			defer span.Finish(tracer.FinishTime(time.Now()))

			cons.cfg.ctx = ctx

			err := next.HandleMessage(message)
			if err != nil {
				span.SetTag("HandleMessage.Error", err)
			}

			return err
		})
	}(handler))
	cons.traceHelper.trace(start, spanTypeConsumer, "AddHandler", nil)
}

func (cons *Consumer) AddConcurrentHandlers(handler nsq.Handler, concurrency int) {
	start := time.Now()
	cons.Consumer.AddConcurrentHandlers(handler, concurrency)
	cons.traceHelper.trace(start, spanTypeConsumer, "AddConcurrentHandlers", nil)
}

func (cons *Consumer) Stop() {
	start := time.Now()
	cons.Consumer.Stop()
	cons.traceHelper.trace(start, spanTypeConsumer, "Stop", nil)
}
