// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package kafka provides functions to trace the confluentinc/confluent-kafka-go package (https://github.com/confluentinc/confluent-kafka-go).
package kafka // import "github.com/DataDog/dd-trace-go/contrib/confluentinc/confluent-kafka-go/kafka.v2/v2"

import (
	"time"

	"github.com/confluentinc/confluent-kafka-go/v2/kafka"

	"github.com/DataDog/dd-trace-go/v2/contrib/confluentinc/confluent-kafka-go/kafkatrace"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

const (
	componentName = instrumentation.PackageConfluentKafkaGoV2
	pkgPath       = "contrib/confluentinc/confluent-kafka-go/kafka.v2"
)

var instr *instrumentation.Instrumentation

func init() {
	instr = instrumentation.Load(componentName)
}

func newKafkaTracer(opts ...Option) *kafkatrace.Tracer {
	v, _ := kafka.LibraryVersion()
	return kafkatrace.NewKafkaTracer(instr, kafkatrace.CKGoVersion2, v, opts...)
}

// NewConsumer calls kafka.NewConsumer and wraps the resulting Consumer.
func NewConsumer(conf *kafka.ConfigMap, opts ...Option) (*Consumer, error) {
	c, err := kafka.NewConsumer(conf)
	if err != nil {
		return nil, err
	}
	opts = append(opts, WithConfig(conf))
	return WrapConsumer(c, opts...), nil
}

// NewProducer calls kafka.NewProducer and wraps the resulting Producer.
func NewProducer(conf *kafka.ConfigMap, opts ...Option) (*Producer, error) {
	p, err := kafka.NewProducer(conf)
	if err != nil {
		return nil, err
	}
	opts = append(opts, WithConfig(conf))
	return WrapProducer(p, opts...), nil
}

// A Consumer wraps a kafka.Consumer.
type Consumer struct {
	*kafka.Consumer
	tracer    *kafkatrace.Tracer
	events    chan kafka.Event
	clusterID *kafkatrace.ClusterIDFetch
}

// WrapConsumer wraps a kafka.Consumer so that any consumed events are traced.
//
// The consumer configuration is not readable from a kafka.Consumer. Pass
// WithConfig with the ConfigMap the consumer was created with to tag the
// consumer group and the bootstrap servers. NewConsumer does this.
func WrapConsumer(c *kafka.Consumer, opts ...Option) *Consumer {
	wrapped := &Consumer{
		Consumer: c,
		tracer:   newKafkaTracer(opts...),
	}
	instr.Logger().Debug("%s: Wrapping Consumer: %#v", pkgPath, wrapped.tracer)
	// The cluster ID is fetched asynchronously for the span tags and data
	// streams monitoring, and the fetch is cancelled on Close to avoid
	// blocking shutdown.
	wrapped.clusterID = kafkatrace.NewClusterIDFetch(wrapped.tracer, func() (kafkatrace.ClusterIDClient, error) {
		// The admin client is created from the consumer handle, so it reuses
		// the consumer's connections and configuration.
		admin, err := kafka.NewAdminClientFromConsumer(c)
		if err != nil {
			return nil, err
		}
		return admin, nil
	})
	wrapped.events = kafkatrace.WrapConsumeEventsChannel(wrapped.tracer, c.Events(), c, func(evt kafka.Event) kafkatrace.Event {
		if _, ok := evt.(*kafka.Message); ok {
			wrapped.clusterID.Start()
		}
		return wrapEvent(evt)
	})
	// Data streams monitoring uses the cluster ID in its checkpoints and
	// backlogs, so fetch it right away to have it as early as possible.
	if wrapped.tracer.DSMEnabled() {
		wrapped.clusterID.Start()
	}
	return wrapped
}

// Close calls the underlying Consumer.Close and if polling is enabled, finishes
// any remaining span.
func (c *Consumer) Close() error {
	// Stop the cluster ID fetch if it is still running
	c.clusterID.Close()
	err := c.Consumer.Close()
	// we only close the previous span if consuming via the events channel is
	// not enabled, because otherwise there would be a data race from the
	// consuming goroutine.
	if c.events == nil && c.tracer.PrevSpan != nil {
		c.tracer.PrevSpan.Finish()
		c.tracer.PrevSpan = nil
	}
	return err
}

// Events returns the kafka Events channel (if enabled). msg events will be
// traced.
func (c *Consumer) Events() chan kafka.Event {
	return c.events
}

// Poll polls the consumer for messages or events. msg will be
// traced.
func (c *Consumer) Poll(timeoutMS int) (event kafka.Event) {
	if c.tracer.PrevSpan != nil {
		c.tracer.PrevSpan.Finish()
		c.tracer.PrevSpan = nil
	}
	evt := c.Consumer.Poll(timeoutMS)
	if msg, ok := evt.(*kafka.Message); ok {
		c.clusterID.Start()
		tMsg := wrapMessage(msg)
		c.tracer.SetConsumeCheckpoint(tMsg)
		c.tracer.PrevSpan = c.tracer.StartConsumeSpan(tMsg)
	} else if offset, ok := evt.(kafka.OffsetsCommitted); ok {
		tOffsets := wrapTopicPartitions(offset.Offsets)
		c.tracer.TrackCommitOffsets(tOffsets, offset.Error)
		c.tracer.TrackHighWatermarkOffset(tOffsets, c.Consumer)
	}
	return evt
}

// ReadMessage polls the consumer for a message. msg will be traced.
func (c *Consumer) ReadMessage(timeout time.Duration) (*kafka.Message, error) {
	if c.tracer.PrevSpan != nil {
		c.tracer.PrevSpan.Finish()
		c.tracer.PrevSpan = nil
	}
	msg, err := c.Consumer.ReadMessage(timeout)
	if err != nil {
		return nil, err
	}
	c.clusterID.Start()
	tMsg := wrapMessage(msg)
	c.tracer.SetConsumeCheckpoint(tMsg)
	c.tracer.PrevSpan = c.tracer.StartConsumeSpan(tMsg)
	return msg, nil
}

// Commit commits current offsets and tracks the commit offsets if data streams is enabled.
func (c *Consumer) Commit() ([]kafka.TopicPartition, error) {
	tps, err := c.Consumer.Commit()
	tOffsets := wrapTopicPartitions(tps)
	c.tracer.TrackCommitOffsets(tOffsets, err)
	c.tracer.TrackHighWatermarkOffset(tOffsets, c.Consumer)
	return tps, err
}

// CommitMessage commits a message and tracks the commit offsets if data streams is enabled.
func (c *Consumer) CommitMessage(msg *kafka.Message) ([]kafka.TopicPartition, error) {
	tps, err := c.Consumer.CommitMessage(msg)
	tOffsets := wrapTopicPartitions(tps)
	c.tracer.TrackCommitOffsets(tOffsets, err)
	c.tracer.TrackHighWatermarkOffset(tOffsets, c.Consumer)
	return tps, err
}

// CommitOffsets commits provided offsets and tracks the commit offsets if data streams is enabled.
func (c *Consumer) CommitOffsets(offsets []kafka.TopicPartition) ([]kafka.TopicPartition, error) {
	tps, err := c.Consumer.CommitOffsets(offsets)
	tOffsets := wrapTopicPartitions(tps)
	c.tracer.TrackCommitOffsets(tOffsets, err)
	c.tracer.TrackHighWatermarkOffset(tOffsets, c.Consumer)
	return tps, err
}

// A Producer wraps a kafka.Producer.
type Producer struct {
	*kafka.Producer
	tracer         *kafkatrace.Tracer
	produceChannel chan *kafka.Message
	events         chan kafka.Event
	clusterID      *kafkatrace.ClusterIDFetch
}

// WrapProducer wraps a kafka.Producer so requests are traced.
//
// The producer configuration is not readable from a kafka.Producer. Pass
// WithConfig with the ConfigMap the producer was created with to tag the
// bootstrap servers. NewProducer does this.
func WrapProducer(p *kafka.Producer, opts ...Option) *Producer {
	wrapped := &Producer{
		Producer: p,
		tracer:   newKafkaTracer(opts...),
		events:   p.Events(),
	}
	instr.Logger().Debug("%s: Wrapping Producer: %#v", pkgPath, wrapped.tracer)
	// The cluster ID is fetched asynchronously for the span tags and data
	// streams monitoring, and the fetch is cancelled on Close to avoid
	// blocking shutdown.
	wrapped.clusterID = kafkatrace.NewClusterIDFetch(wrapped.tracer, func() (kafkatrace.ClusterIDClient, error) {
		// The admin client is created from the producer handle, so it reuses
		// the producer's connections and configuration.
		admin, err := kafka.NewAdminClientFromProducer(p)
		if err != nil {
			return nil, err
		}
		return admin, nil
	})
	wrapped.produceChannel = kafkatrace.WrapProduceChannel(wrapped.tracer, p.ProduceChannel(), func(msg *kafka.Message) kafkatrace.Message {
		wrapped.clusterID.Start()
		return wrapMessage(msg)
	})
	if wrapped.tracer.DSMEnabled() {
		wrapped.events = kafkatrace.WrapProduceEventsChannel(wrapped.tracer, p.Events(), wrapEvent)
		// Data streams monitoring uses the cluster ID in its checkpoints and
		// backlogs, so fetch it right away to have it as early as possible.
		wrapped.clusterID.Start()
	}
	return wrapped
}

// Events returns the kafka Events channel (if enabled). msg events will be monitored
// with data streams monitoring (if enabled)
func (p *Producer) Events() chan kafka.Event {
	return p.events
}

// Close calls the underlying Producer.Close and also closes the internal
// wrapping producer channel.
func (p *Producer) Close() {
	// Stop the cluster ID fetch if it is still running
	p.clusterID.Close()
	close(p.produceChannel)
	p.Producer.Close()
}

// Produce calls the underlying Producer.Produce and traces the request.
func (p *Producer) Produce(msg *kafka.Message, deliveryChan chan kafka.Event) error {
	p.clusterID.Start()
	tMsg := wrapMessage(msg)
	span := p.tracer.StartProduceSpan(tMsg)

	var errChan chan error

	if deliveryChan == nil {
		deliveryChan = p.events
	}

	deliveryChan, errChan = kafkatrace.WrapDeliveryChannel(p.tracer, deliveryChan, span, wrapEvent)

	p.tracer.SetProduceCheckpoint(tMsg)

	err := p.Producer.Produce(msg, deliveryChan)
	if err != nil {
		if errChan != nil {
			errChan <- err
		} else {
			// with no delivery channel or enqueue error, finish immediately
			span.Finish(tracer.WithError(err))
		}
	}
	return err
}

// ProduceChannel returns a channel which can receive kafka Messages and will
// send them to the underlying producer channel.
func (p *Producer) ProduceChannel() chan *kafka.Message {
	return p.produceChannel
}
