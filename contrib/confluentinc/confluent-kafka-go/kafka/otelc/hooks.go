// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package otelc holds the otelc rules and hooks for
// github.com/confluentinc/confluent-kafka-go/kafka. See otelc.yaml. It is a
// separate module so otelc never reaches contrib users who do not use it.
package otelc

import (
	"runtime"
	"sync"
	"weak"

	"github.com/confluentinc/confluent-kafka-go/kafka"
	"go.opentelemetry.io/otelc/pkg/hook"

	kafkatrace "github.com/DataDog/dd-trace-go/contrib/confluentinc/confluent-kafka-go/kafka/v2"
)

// consumers and producers hold the contrib wrapper of each client that
// TraceConsumer or TraceProducer registered. The values hold no reference to
// their client, and an entry is deleted once its client is freed.
var (
	consumers sync.Map // weak.Pointer[kafka.Consumer] -> *kafkatrace.Consumer with a nil Consumer
	producers sync.Map // weak.Pointer[kafka.Producer] -> *kafkatrace.Producer with a nil Producer
)

// TraceConsumer makes the hooks trace c. The otelc rules call it where the
// application calls kafka.NewConsumer, with the same conf. The hooks leave
// consumers that were not registered alone, such as those the contrib builds.
func TraceConsumer(c *kafka.Consumer, conf *kafka.ConfigMap) {
	var opts []kafkatrace.Option
	if conf != nil {
		opts = append(opts, kafkatrace.WithConfig(conf))
	}
	w := kafkatrace.WrapConsumer(c, opts...)
	w.Consumer = nil
	register(&consumers, c, w)
}

// TraceProducer makes the hooks trace p. The otelc rules call it where the
// application calls kafka.NewProducer, with the same conf. The hooks leave
// producers that were not registered alone, such as those the contrib builds.
func TraceProducer(p *kafka.Producer, conf *kafka.ConfigMap) {
	var opts []kafkatrace.Option
	if conf != nil {
		opts = append(opts, kafkatrace.WithConfig(conf))
	}
	w := kafkatrace.WrapProducer(p, opts...)
	w.Producer = nil
	register(&producers, p, w)
}

func register[T, W any](m *sync.Map, client *T, w *W) {
	key := weak.Make(client)
	if _, loaded := m.LoadOrStore(key, w); !loaded {
		runtime.AddCleanup(client, func(key weak.Pointer[T]) { m.Delete(key) }, key)
	}
}

// tracedConsumer returns the contrib wrapper of c, or nil when c was not
// registered.
func tracedConsumer(c *kafka.Consumer) *kafkatrace.Consumer {
	if c == nil {
		return nil
	}
	v, ok := consumers.Load(weak.Make(c))
	if !ok {
		return nil
	}
	w := *v.(*kafkatrace.Consumer)
	w.Consumer = c
	return &w
}

// tracedProducer returns the contrib wrapper of p, or nil when p was not
// registered.
func tracedProducer(p *kafka.Producer) *kafkatrace.Producer {
	if p == nil {
		return nil
	}
	v, ok := producers.Load(weak.Make(p))
	if !ok {
		return nil
	}
	w := *v.(*kafkatrace.Producer)
	w.Producer = p
	return &w
}

// Each before hook passes the contrib wrapper of a registered client to its
// after hook, and does nothing for other clients.

// BeforeConsumerPoll finishes the span of the previous message. Poll also runs
// inside kafka.Consumer.ReadMessage and kafka.Consumer.Close.
func BeforeConsumerPoll(ictx hook.HookContext, c *kafka.Consumer, _ int) {
	if w := tracedConsumer(c); w != nil {
		w.BeforePoll()
		ictx.SetData(w)
	}
}

// AfterConsumerPoll starts the span of the message Poll returned.
func AfterConsumerPoll(ictx hook.HookContext, evt kafka.Event) {
	if w, ok := ictx.GetData().(*kafkatrace.Consumer); ok {
		w.AfterPoll(evt)
	}
}

// BeforeConsumerEvents passes the contrib wrapper of c to AfterConsumerEvents.
func BeforeConsumerEvents(ictx hook.HookContext, c *kafka.Consumer) {
	setConsumer(ictx, c)
}

// AfterConsumerEvents returns the traced events channel.
func AfterConsumerEvents(ictx hook.HookContext, _ chan kafka.Event) {
	if w, ok := ictx.GetData().(*kafkatrace.Consumer); ok {
		ictx.SetReturnVal(0, w.Events())
	}
}

// BeforeConsumerCommit passes the contrib wrapper of c to AfterConsumerCommit.
func BeforeConsumerCommit(ictx hook.HookContext, c *kafka.Consumer) {
	setConsumer(ictx, c)
}

// AfterConsumerCommit tracks the committed offsets.
func AfterConsumerCommit(ictx hook.HookContext, tps []kafka.TopicPartition, err error) {
	afterCommit(ictx, tps, err)
}

// BeforeConsumerCommitMessage passes the contrib wrapper of c to
// AfterConsumerCommitMessage.
func BeforeConsumerCommitMessage(ictx hook.HookContext, c *kafka.Consumer, _ *kafka.Message) {
	setConsumer(ictx, c)
}

// AfterConsumerCommitMessage tracks the committed offsets.
func AfterConsumerCommitMessage(ictx hook.HookContext, tps []kafka.TopicPartition, err error) {
	afterCommit(ictx, tps, err)
}

// BeforeConsumerCommitOffsets passes the contrib wrapper of c to
// AfterConsumerCommitOffsets.
func BeforeConsumerCommitOffsets(ictx hook.HookContext, c *kafka.Consumer, _ []kafka.TopicPartition) {
	setConsumer(ictx, c)
}

// AfterConsumerCommitOffsets tracks the committed offsets.
func AfterConsumerCommitOffsets(ictx hook.HookContext, tps []kafka.TopicPartition, err error) {
	afterCommit(ictx, tps, err)
}

func afterCommit(ictx hook.HookContext, tps []kafka.TopicPartition, err error) {
	if w, ok := ictx.GetData().(*kafkatrace.Consumer); ok {
		w.AfterCommit(tps, err)
	}
}

// BeforeConsumerClose stops the async jobs of the contrib wrapper.
func BeforeConsumerClose(ictx hook.HookContext, c *kafka.Consumer) {
	if w := tracedConsumer(c); w != nil {
		w.BeforeClose()
		ictx.SetData(w)
	}
}

// AfterConsumerClose finishes the remaining span.
func AfterConsumerClose(ictx hook.HookContext, _ error) {
	if w, ok := ictx.GetData().(*kafkatrace.Consumer); ok {
		w.AfterClose()
	}
}

func setConsumer(ictx hook.HookContext, c *kafka.Consumer) {
	if w := tracedConsumer(c); w != nil {
		ictx.SetData(w)
	}
}

// BeforeProducerProduce starts the span and passes the traced delivery channel
// on to the original call.
func BeforeProducerProduce(ictx hook.HookContext, p *kafka.Producer, msg *kafka.Message, deliveryChan chan kafka.Event) {
	w := tracedProducer(p)
	if w == nil {
		return
	}
	deliveryChan, afterProduce := w.BeforeProduce(msg, deliveryChan)
	// Parameter 0 is the receiver.
	ictx.SetParam(2, deliveryChan)
	ictx.SetData(afterProduce)
}

// AfterProducerProduce finishes the span when Produce fails.
func AfterProducerProduce(ictx hook.HookContext, err error) {
	if afterProduce, ok := ictx.GetData().(func(error)); ok {
		afterProduce(err)
	}
}

// BeforeProducerEvents passes the contrib wrapper of p to AfterProducerEvents.
func BeforeProducerEvents(ictx hook.HookContext, p *kafka.Producer) {
	setProducer(ictx, p)
}

// AfterProducerEvents returns the events channel of the contrib wrapper,
// which tracks produced offsets when data streams is enabled.
func AfterProducerEvents(ictx hook.HookContext, _ chan kafka.Event) {
	if w, ok := ictx.GetData().(*kafkatrace.Producer); ok {
		ictx.SetReturnVal(0, w.Events())
	}
}

// BeforeProducerProduceChannel passes the contrib wrapper of p to
// AfterProducerProduceChannel.
func BeforeProducerProduceChannel(ictx hook.HookContext, p *kafka.Producer) {
	setProducer(ictx, p)
}

// AfterProducerProduceChannel returns the traced produce channel.
func AfterProducerProduceChannel(ictx hook.HookContext, _ chan *kafka.Message) {
	if w, ok := ictx.GetData().(*kafkatrace.Producer); ok {
		ictx.SetReturnVal(0, w.ProduceChannel())
	}
}

// BeforeProducerClose closes the traced produce channel.
func BeforeProducerClose(_ hook.HookContext, p *kafka.Producer) {
	if w := tracedProducer(p); w != nil {
		w.BeforeClose()
	}
}

func setProducer(ictx hook.HookContext, p *kafka.Producer) {
	if w := tracedProducer(p); w != nil {
		ictx.SetData(w)
	}
}
