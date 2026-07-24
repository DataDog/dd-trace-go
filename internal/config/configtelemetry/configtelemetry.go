// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

// Package configtelemetry provides the telemetry reporting functions for configuration values.
//
// All configuration telemetry must go through the three exported functions in this package.
//
//   - [Report]: use to report a non-default value (auto-increments seqID)
//   - [ReportWithID]: same as Report, but also records the config source's ID
//   - [ReportDefault]: use to report the hard-coded default for a configuration
package configtelemetry

import (
	"sync/atomic"

	"github.com/DataDog/dd-trace-go/v2/internal/env"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
)

// defaultSeqID is the sequence ID used for all default configuration values.
// Non-default values always have a SeqID strictly greater than defaultSeqID.
const defaultSeqID uint64 = 1

var seqID atomic.Uint64

func init() {
	seqID.Store(defaultSeqID)
}

// nextSeqID returns a new sequence ID, strictly greater than defaultSeqID.
// It must only be called through Prepare or PrepareWithID.
func nextSeqID() uint64 {
	return seqID.Add(1)
}

// Prepared is a configuration report whose sequence ID was reserved when the
// corresponding state transition was accepted. Submit may safely run later,
// after the caller releases its state lock.
type Prepared struct {
	name   string
	origin telemetry.Origin
	id     string
	seqID  uint64
	send   bool
}

// Submit sends the prepared report with value.
func (p Prepared) Submit(value any) {
	if !p.send {
		return
	}
	telemetry.RegisterAppConfigs(telemetry.Configuration{
		Name:   p.name,
		Value:  value,
		Origin: p.origin,
		ID:     p.id,
		SeqID:  p.seqID,
	})
}

func prepare(name string, origin telemetry.Origin, id string, sequence uint64) Prepared {
	return Prepared{
		name:   name,
		origin: origin,
		id:     id,
		seqID:  sequence,
		send:   !env.IsSensitive(name),
	}
}

// Prepare reserves a report for a non-default configuration source.
func Prepare(name string, origin telemetry.Origin) Prepared {
	return prepare(name, origin, telemetry.EmptyID, nextSeqID())
}

// PrepareWithID reserves a non-default report carrying a source config ID.
func PrepareWithID(name string, origin telemetry.Origin, id string) Prepared {
	return prepare(name, origin, id, nextSeqID())
}

// PrepareDefault prepares a hard-coded default report without advancing the
// shared non-default sequence counter.
func PrepareDefault(name string) Prepared {
	return prepare(name, telemetry.OriginDefault, telemetry.EmptyID, defaultSeqID)
}

// Report reports a configuration value from a non-default configuration source.
func Report(name string, value any, origin telemetry.Origin) {
	Prepare(name, origin).Submit(value)
}

// ReportWithID reports a non-default configuration value, including the config source's ID.
// Use this for sources that carry a config_id (e.g. declarative config).
func ReportWithID(name string, value any, origin telemetry.Origin, id string) {
	PrepareWithID(name, origin, id).Submit(value)
}

// ReportDefault reports the value for a configuration key from the 'default' configuration source.
func ReportDefault(name string, value any) {
	PrepareDefault(name).Submit(value)
}
