// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

// Package configtelemetry provides the telemetry reporting functions for configuration values.
//
// # Usage Contract
//
// All configuration telemetry must go through the three exported functions in this package.
// Do not access seqID or call nextSeqID directly — the exported functions encode the correct
// behavior for each case:
//
//   - [Report]: non-default value set by a user or the system (auto-increments sequence ID)
//   - [ReportWithID]: same as Report, but also records the config source's ID
//   - [ReportDefault]: the hard-coded default for a key (always uses [DefaultSeqID])
//
// DefaultSeqID is exported for tests that need to assert on sequence ID ordering.
package configtelemetry

import (
	"sync/atomic"

	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
)

// DefaultSeqID is the sequence ID used for all default configuration values.
// Non-default values always have a SeqID strictly greater than DefaultSeqID.
const DefaultSeqID uint64 = 1

var seqID atomic.Uint64

func init() {
	seqID.Store(DefaultSeqID)
}

// nextSeqID returns a new sequence ID, strictly greater than DefaultSeqID.
// It must only be called through Report or ReportWithID.
func nextSeqID() uint64 {
	return seqID.Add(1)
}

// Report reports a non-default configuration value with an auto-incremented sequence ID.
func Report(name string, value any, origin telemetry.Origin) {
	telemetry.RegisterAppConfigs(telemetry.Configuration{
		Name:   name,
		Value:  value,
		Origin: origin,
		ID:     telemetry.EmptyID,
		SeqID:  nextSeqID(),
	})
}

// ReportWithID reports a non-default configuration value, including the config source's ID,
// with an auto-incremented sequence ID. Use this for sources that carry a config_id
// (e.g. declarative config files).
func ReportWithID(name string, value any, origin telemetry.Origin, id string) {
	telemetry.RegisterAppConfigs(telemetry.Configuration{
		Name:   name,
		Value:  value,
		Origin: origin,
		ID:     id,
		SeqID:  nextSeqID(),
	})
}

// ReportDefault reports the hard-coded default value for a configuration key.
// Defaults always use DefaultSeqID so they sort before any user-supplied value.
func ReportDefault(name string, value any) {
	telemetry.RegisterAppConfigs(telemetry.Configuration{
		Name:   name,
		Value:  value,
		Origin: telemetry.OriginDefault,
		ID:     telemetry.EmptyID,
		SeqID:  DefaultSeqID,
	})
}
