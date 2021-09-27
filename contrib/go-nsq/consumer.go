// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2021 Datadog, Inc.
// Author: CodapeWild (https://github.com/CodapeWild/)

package nsq

import (
	"fmt"
	"math"
	"strings"

	"github.com/nsqio/go-nsq"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

// Consumer is a wrap-up class of nsq Consumer.
type Consumer struct {
	resource string
	*nsq.Consumer
	cfg *clientConfig
}

// NewConsumer return a new nsq Consumer wrapped with tracing.
func NewConsumer(topic string, channel string, config *nsq.Config, opts ...Option) (*Consumer, error) {
	consu, err := nsq.NewConsumer(topic, channel, config)
	if err != nil {
		return nil, err
	}

	cfg := &clientConfig{}
	defaultConfig(cfg)
	for _, opt := range opts {
		opt(cfg)
	}

	return &Consumer{
		resource: fmt.Sprintf("[topic:%s, channel:%s]", topic, channel),
		Consumer: consu,
		cfg:      cfg,
	}, nil
}

// ConnectToNSQLookupd is a nsq Consumer ConnectToNSQLookupd wrapper with tracing.
func (consu *Consumer) ConnectToNSQLookupd(addr string) error {
	var (
		opName = "ConnectToNSQLookupd"
		span   = consu.startSpan(opName)
		err    = consu.Consumer.ConnectToNSQLookupd(addr)
		tags   = map[string]interface{}{
			"lookupd_addr": addr,
		}
	)
	consu.finishSpan(span, opName, tags, err)

	return err
}

// ConnectToNSQLookupds is a nsq Consumer ConnectToNSQLookupds wrapper with tracing.
func (consu *Consumer) ConnectToNSQLookupds(addrs []string) error {
	var (
		opName = "ConnectToNSQLookupds"
		span   = consu.startSpan(opName)
		err    = consu.Consumer.ConnectToNSQLookupds(addrs)
		tags   = map[string]interface{}{
			"lookupd_addrs": strings.Join(addrs, ","),
			"lookupd_count": len(addrs),
		}
	)
	consu.finishSpan(span, opName, tags, err)

	return err
}

// ConnectToNSQD is a nsq Consumer ConnectToNSQD wrapper with tracing.
func (consu *Consumer) ConnectToNSQD(addr string) error {
	var (
		opName = "ConnectToNSQD"
		span   = consu.startSpan(opName)
		err    = consu.Consumer.ConnectToNSQD(addr)
		tags   = map[string]interface{}{
			"nsqd_addr": addr,
		}
	)
	consu.finishSpan(span, opName, tags, err)

	return err
}

// ConnectToNSQDs is a nsq Consumer ConnectToNSQDs wrapper with tracing.
func (consu *Consumer) ConnectToNSQDs(addrs []string) error {
	var (
		opName = "ConnectToNSQDs"
		span   = consu.startSpan(opName)
		err    = consu.Consumer.ConnectToNSQDs(addrs)
		tags   = map[string]interface{}{
			"nsqd_addrs": strings.Join(addrs, ","),
			"nsqd_count": len(addrs),
		}
	)
	consu.finishSpan(span, opName, tags, err)

	return err
}

// DisconnectFromNSQD is a nsq Consumer DisconnectFromNSQD wrapper with tracing.
func (consu *Consumer) DisconnectFromNSQD(addr string) error {
	var (
		opName = "DisconnectFromNSQD"
		span   = consu.startSpan(opName)
		err    = consu.Consumer.ConnectToNSQLookupd(addr)
		tags   = map[string]interface{}{
			"nsqd_addr": addr,
		}
	)
	consu.finishSpan(span, opName, tags, err)

	return err
}

// DisconnectFromNSQLookupd is a nsq Consumer DisconnectFromNSQLookupd wrapper with tracing.
func (consu *Consumer) DisconnectFromNSQLookupd(addr string) error {
	var (
		opName = "DisconnectFromNSQLookupd"
		span   = consu.startSpan(opName)
		err    = consu.Consumer.DisconnectFromNSQLookupd(addr)
		tags   = map[string]interface{}{
			"lookupd_addr": addr,
		}
	)
	consu.finishSpan(span, opName, tags, err)

	return err
}

// AddHandler is a nsq Consumer Addhandler wrapper with tracing operations injected into the original registered handler.
func (consu *Consumer) AddHandler(handler nsq.Handler) {
	consu.Consumer.AddHandler(nsq.HandlerFunc(func(message *nsq.Message) error {
		var (
			opName = "nsq_message_handler"
			span   = consu.startSpan(opName)
			err    = handler.HandleMessage(message)
			stats  = consu.Stats()
			tags   = map[string]interface{}{
				"connections":       stats.Connections,
				"message_received":  stats.MessagesReceived,
				"message_finished":  stats.MessagesFinished,
				"message_requeued":  stats.MessagesRequeued,
				"starved":           consu.IsStarved(),
				"message_attempts":  message.Attempts,
				"message_body_size": len(message.Body),
				"message_timestamp": message.Timestamp,
				"nsqd_addr":         message.NSQDAddress,
			}
		)
		consu.finishSpan(span, opName, tags, err)

		return err
	}))
}

// AddConcurrentHandlers is a nsq Consumer AddConcurrentHandlers wrapper with tracing operations injected into the original registered handler.
func (consu *Consumer) AddConcurrentHandlers(handler nsq.Handler, concurrency int) {
	consu.AddConcurrentHandlers(nsq.HandlerFunc(func(message *nsq.Message) error {
		var (
			opName = "nsq_message_handler"
			span   = consu.startSpan(opName)
			err    = handler.HandleMessage(message)
			stats  = consu.Stats()
			tags   = map[string]interface{}{
				"connections":       stats.Connections,
				"message_received":  stats.MessagesReceived,
				"message_finished":  stats.MessagesFinished,
				"message_requeued":  stats.MessagesRequeued,
				"starved":           consu.IsStarved(),
				"message_attempts":  message.Attempts,
				"message_body_size": len(message.Body),
				"message_timestamp": message.Timestamp,
				"nsqd_addr":         message.NSQDAddress,
				"concurrency":       concurrency,
			}
		)
		consu.finishSpan(span, opName, tags, err)

		return err
	}), concurrency)
}

func (consu *Consumer) startSpan(operation string) ddtrace.Span {
	opts := []ddtrace.StartSpanOption{
		tracer.SpanType(ext.SpanTypeMessageConsumer),
		tracer.ServiceName(consu.cfg.service),
		tracer.ResourceName(consu.resource),
	}
	if !math.IsNaN(consu.cfg.analyticsRate) {
		opts = append(opts, tracer.Tag(ext.EventSampleRate, consu.cfg.analyticsRate))
	}

	span, _ := tracer.StartSpanFromContext(consu.cfg.ctx, operation, opts...)

	return span
}

func (consu *Consumer) finishSpan(span ddtrace.Span, operation string, tags map[string]interface{}, err error) {
	span.SetOperationName(operation)
	for k, v := range tags {
		span.SetTag(k, v)
	}
	span.SetTag(ext.ResourceName, consu.resource)
	var opts []ddtrace.FinishOption
	if err != nil {
		opts = append(opts, tracer.WithError(err))
	}
	span.Finish(opts...)
}
