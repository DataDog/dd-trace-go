// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package namingschema

import "fmt"

// MessagingSystem represents messaging systems to be used for messaging naming schemas in this package.
type MessagingSystem string

const (
	// MessagingSystemKafka represents Kafka.
	MessagingSystemKafka MessagingSystem = "kafka"
)

type messagingOutboundOperationNameSchema struct {
	cfg    *config
	system MessagingSystem
}

// NewMessagingOutboundOperationNameSchema creates a new naming schema for outbound operations from messaging systems.
func NewMessagingOutboundOperationNameSchema(system MessagingSystem, opts ...Option) *Schema {
	cfg := &config{}
	for _, opt := range opts {
		opt(cfg)
	}
	return New(&messagingOutboundOperationNameSchema{cfg: cfg, system: system})
}

func (m *messagingOutboundOperationNameSchema) V0() string {
	if f, ok := m.cfg.versionOverrides[SchemaV0]; ok {
		return f()
	}
	return m.V1()
}

func (m *messagingOutboundOperationNameSchema) V1() string {
	return fmt.Sprintf("%s.send", m.system)
}

type messagingInboundOperationNameSchema struct {
	cfg    *config
	system MessagingSystem
}

// NewMessagingInboundOperationNameSchema creates a new schema for inbound operations from messaging systems.
func NewMessagingInboundOperationNameSchema(system MessagingSystem, opts ...Option) *Schema {
	cfg := &config{}
	for _, opt := range opts {
		opt(cfg)
	}
	return New(&messagingInboundOperationNameSchema{cfg: cfg, system: system})
}

func (m *messagingInboundOperationNameSchema) V0() string {
	if f, ok := m.cfg.versionOverrides[SchemaV0]; ok {
		return f()
	}
	return m.V1()
}

func (m *messagingInboundOperationNameSchema) V1() string {
	return fmt.Sprintf("%s.process", m.system)
}
