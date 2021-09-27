// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package types

import (
	"gopkg.in/DataDog/dd-trace-go.v1/internal/dyngo"
)

type (
	SecurityEvent struct {
		Event   interface{}
		Context []SecurityEventContext
	}

	SecurityEventContext interface {
		isSecurityEventContext()
	}
)

// OnSecurityEventDataListener is a helper function to create an operation data listener of *SecurityEvent values.
func OnSecurityEventDataListener(l func(*dyngo.Operation, *SecurityEvent)) dyngo.EventListener {
	return dyngo.OnDataEventListener((**SecurityEvent)(nil), func(op *dyngo.Operation, v interface{}) {
		l(op, v.(*SecurityEvent))
	})
}

// OnSecurityEventData is a helper function to listen to operation data events of *SecurityEvent values.
func OnSecurityEventData(op *dyngo.Operation, l func(*dyngo.Operation, *SecurityEvent)) {
	op.OnData((**SecurityEvent)(nil), func(op *dyngo.Operation, v interface{}) {
		l(op, v.(*SecurityEvent))
	})
}

func NewSecurityEvent(event interface{}, ctx ...SecurityEventContext) *SecurityEvent {
	return &SecurityEvent{
		Event:   event,
		Context: ctx,
	}
}

func (e *SecurityEvent) AddContext(ctx ...SecurityEventContext) {
	e.Context = append(e.Context, ctx...)
}

type (
	HTTPOperationContext struct {
		Request  HTTPRequestContext
		Response HTTPResponseContext
	}

	HTTPRequestContext struct {
		Method     string
		Host       string
		IsTLS      bool
		RequestURI string
		RemoteAddr string
	}

	HTTPResponseContext struct {
		Status int
	}
)

func (HTTPOperationContext) isSecurityEventContext() {}

type (
	SpanContext struct {
		TraceID, SpanID uint64
	}
)

func (SpanContext) isSecurityEventContext() {}

type ServiceContext struct {
	Name, Version, Environment string
}

func (ServiceContext) isSecurityEventContext() {}

type TagContext []string

func (TagContext) isSecurityEventContext() {}

type TracerContext struct {
	Runtime, RuntimeVersion, Version string
}

func (TracerContext) isSecurityEventContext() {}

type HostContext struct {
	Hostname, OS string
}

func (HostContext) isSecurityEventContext() {}
