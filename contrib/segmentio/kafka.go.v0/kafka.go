// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package kafka // import "gopkg.in/DataDog/dd-trace-go.v1/contrib/segmentio/kafka.go.v0"

import (
	v2 "github.com/DataDog/dd-trace-go/contrib/segmentio/kafka-go/v2"

	"github.com/segmentio/kafka-go"
)

// NewReader calls kafka.NewReader and wraps the resulting Consumer.
func NewReader(conf kafka.ReaderConfig, opts ...Option) *Reader {
	return v2.NewReader(conf, opts...)
}

// NewWriter calls kafka.NewWriter and wraps the resulting Producer.
func NewWriter(conf kafka.WriterConfig, opts ...Option) *Writer {
	return v2.NewWriter(conf, opts...)
}

// WrapReader wraps a kafka.Reader so that any consumed events are traced.
func WrapReader(c *kafka.Reader, opts ...Option) *Reader {
	return v2.WrapReader(c, opts...)
}

// A Reader wraps a kafka.Reader.
type Reader = v2.Reader

// WrapWriter wraps a kafka.Writer so requests are traced.
func WrapWriter(w *kafka.Writer, opts ...Option) *Writer {
	return v2.WrapWriter(w, opts...)
}

// Writer wraps a kafka.Writer with tracing config data
type Writer = v2.KafkaWriter
