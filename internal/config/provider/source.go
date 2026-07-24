// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package provider

import "github.com/DataDog/dd-trace-go/v2/internal/config/schema"

// LookupSource is a configuration source that preserves the distinction
// between an absent key and a present key whose value is empty.
//
// The unexported methods intentionally keep source implementations inside this
// package while allowing the resolution API to name its source contract.
type LookupSource interface {
	lookup(key string) (raw string, present bool)
	origin() schema.Origin
}

// configSource is retained as an internal alias while callers migrate to named
// binding resolvers.
type configSource = LookupSource

type idAwareConfigSource interface {
	LookupSource
	getID() string
}

type environmentConfigSource interface {
	LookupSource
	environmentSource() bool
}

type eventLookupSource interface {
	LookupSource
	lookupWithEvents(key string) (raw string, present bool, applicable bool, err error, events []ConfigEvent)
}

// EventKind identifies configuration state and provider diagnostics.
type EventKind = schema.EventKind

const (
	EventConfiguration  = schema.EventConfiguration
	EventOTelEnvHiding  = schema.EventOTelEnvHiding
	EventOTelEnvInvalid = schema.EventOTelEnvInvalid
)

// ReportCadence controls deduplication by the configuration reporter.
type ReportCadence = schema.ReportCadence

const (
	ReportNever             = schema.ReportNever
	ReportOncePerGeneration = schema.ReportOncePerGeneration
	ReportOnChange          = schema.ReportOnChange
)

// ConfigEvent is the dependency-leaf event model shared with the reporter.
type ConfigEvent = schema.ConfigEvent

func cadenceFor(binding schema.ConsumerBinding) ReportCadence {
	if binding.Sampling == schema.SamplePerCall {
		return ReportOnChange
	}
	return ReportOncePerGeneration
}
