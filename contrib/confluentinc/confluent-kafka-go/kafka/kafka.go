// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package kafka provides functions to trace the confluentinc/confluent-kafka-go package (https://github.com/confluentinc/confluent-kafka-go).
package kafka // import "github.com/DataDog/dd-trace-go/contrib/confluentinc/confluent-kafka-go/kafka/v2"

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/confluentinc/confluent-kafka-go/kafka"

	"github.com/DataDog/dd-trace-go/v2/contrib/confluentinc/confluent-kafka-go/kafkatrace"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

const (
	componentName = instrumentation.PackageConfluentKafkaGo
	pkgPath       = "contrib/confluentinc/confluent-kafka-go/kafka"
)

var instr *instrumentation.Instrumentation

func init() {
	instr = instrumentation.Load(componentName)
}

func newKafkaTracer(opts ...Option) *kafkatrace.Tracer {
	v, _ := kafka.LibraryVersion()
	return kafkatrace.NewKafkaTracer(instr, kafkatrace.CKGoVersion1, v, opts...)
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

// startClusterIDFetch launches a goroutine to fetch the cluster ID from the
// given admin client. It returns a stop function that cancels the fetch and
// waits for the goroutine to exit.
func startClusterIDFetch(tr *kafkatrace.Tracer, admin *kafka.AdminClient) func() {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer admin.Close()
		ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		clusterID, err := admin.ClusterID(ctx)
		if err != nil {
			if ctx.Err() == context.Canceled {
				return
			}
			instr.Logger().Warn("failed to fetch Kafka cluster ID: %s", err)
			return
		}
		tr.SetClusterID(clusterID)
	}()
	return func() {
		cancel()
		<-done
	}
}

// clusterIDFetch starts the cluster ID lookup of a wrapped client at most
// once, and stops it on Close. Without data streams monitoring, the lookup
// starts on the first produce or consume, so that a client closed without
// being used does not wait for it in Close.
type clusterIDFetch struct {
	tracer   *kafkatrace.Tracer
	newAdmin func() (*kafka.AdminClient, error)

	started atomic.Bool // fast path for start, set under mu
	mu      sync.Mutex
	closed  bool   // +checklocks:mu
	stop    func() // +checklocks:mu
}

// start starts the cluster ID lookup unless it was already started or the
// client is closed. It is cheap to call on every message.
func (f *clusterIDFetch) start() {
	if f.started.Load() {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed || f.started.Load() {
		return
	}
	f.started.Store(true)
	// The admin client is created from the client handle, so it reuses the
	// client's connections and configuration.
	admin, err := f.newAdmin()
	if err != nil {
		instr.Logger().Warn("failed to create admin client for cluster ID, not adding cluster_id tags: %s", err)
		return
	}
	f.stop = startClusterIDFetch(f.tracer, admin)
}

// close prevents the lookup from starting, and cancels and waits for it if it
// is running.
func (f *clusterIDFetch) close() {
	f.mu.Lock()
	f.closed = true
	stop := f.stop
	f.stop = nil
	f.mu.Unlock()
	if stop != nil {
		stop()
	}
}

// A Consumer wraps a kafka.Consumer.
type Consumer struct {
	*kafka.Consumer
	tracer    *kafkatrace.Tracer
	events    chan kafka.Event
	clusterID *clusterIDFetch
}

// WrapConsumer wraps a kafka.Consumer so that any consumed events are traced.
func WrapConsumer(c *kafka.Consumer, opts ...Option) *Consumer {
	wrapped := &Consumer{
		Consumer: c,
		tracer:   newKafkaTracer(opts...),
	}
	instr.Logger().Debug("%s: Wrapping Consumer: %#v", pkgPath, wrapped.tracer)
	// The cluster ID is fetched asynchronously for the span tags and data
	// streams monitoring, and the fetch is cancelled on Close to avoid
	// blocking shutdown.
	wrapped.clusterID = &clusterIDFetch{
		tracer:   wrapped.tracer,
		newAdmin: func() (*kafka.AdminClient, error) { return kafka.NewAdminClientFromConsumer(c) },
	}
	wrapped.events = kafkatrace.WrapConsumeEventsChannel(wrapped.tracer, c.Events(), c, func(evt kafka.Event) kafkatrace.Event {
		if _, ok := evt.(*kafka.Message); ok {
			wrapped.clusterID.start()
		}
		return wrapEvent(evt)
	})
	// Data streams monitoring uses the cluster ID in its checkpoints and
	// backlogs, so fetch it right away to have it as early as possible.
	if wrapped.tracer.DSMEnabled() {
		wrapped.clusterID.start()
	}
	return wrapped
}

// Close calls the underlying Consumer.Close and if polling is enabled, finishes
// any remaining span.
func (c *Consumer) Close() error {
	// Stop the cluster ID fetch if it is still running
	c.clusterID.close()
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
		c.clusterID.start()
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
	c.clusterID.start()
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
	clusterID      *clusterIDFetch
}

// WrapProducer wraps a kafka.Producer so requests are traced.
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
	wrapped.clusterID = &clusterIDFetch{
		tracer:   wrapped.tracer,
		newAdmin: func() (*kafka.AdminClient, error) { return kafka.NewAdminClientFromProducer(p) },
	}
	wrapped.produceChannel = kafkatrace.WrapProduceChannel(wrapped.tracer, p.ProduceChannel(), func(msg *kafka.Message) kafkatrace.Message {
		wrapped.clusterID.start()
		return wrapMessage(msg)
	})
	if wrapped.tracer.DSMEnabled() {
		wrapped.events = kafkatrace.WrapProduceEventsChannel(wrapped.tracer, p.Events(), wrapEvent)
		// Data streams monitoring uses the cluster ID in its checkpoints and
		// backlogs, so fetch it right away to have it as early as possible.
		wrapped.clusterID.start()
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
	p.clusterID.close()
	close(p.produceChannel)
	p.Producer.Close()
}

// Produce calls the underlying Producer.Produce and traces the request.
func (p *Producer) Produce(msg *kafka.Message, deliveryChan chan kafka.Event) error {
	p.clusterID.start()
	tMsg := wrapMessage(msg)
	span := p.tracer.StartProduceSpan(tMsg)

	var errChan chan error
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
