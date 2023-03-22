// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package namingschema

import "fmt"

type MessagingSystem string

const (
	MessagingSystemKafka MessagingSystem = "kafka"
)

type messagingOutboundOperationNameSchema struct {
	cfg    *config
	system MessagingSystem
}

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
