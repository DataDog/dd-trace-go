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
// It must only be called through Report or ReportWithID.
func nextSeqID() uint64 {
	return seqID.Add(1)
}

// Report reports a configuration value from a non-default configuration source.
func Report(name string, value any, origin telemetry.Origin) {
	telemetry.RegisterAppConfigs(telemetry.Configuration{
		Name:   name,
		Value:  value,
		Origin: origin,
		ID:     telemetry.EmptyID,
		SeqID:  nextSeqID(),
	})
}

// ReportWithID reports a non-default configuration value, including the config source's ID.
// Use this for sources that carry a config_id (e.g. declarative config).
func ReportWithID(name string, value any, origin telemetry.Origin, id string) {
	telemetry.RegisterAppConfigs(telemetry.Configuration{
		Name:   name,
		Value:  value,
		Origin: origin,
		ID:     id,
		SeqID:  nextSeqID(),
	})
}

// ReportDefault reports the value for a configuration key from the 'default' configuration source.
func ReportDefault(name string, value any) {
	telemetry.RegisterAppConfigs(telemetry.Configuration{
		Name:   name,
		Value:  value,
		Origin: telemetry.OriginDefault,
		ID:     telemetry.EmptyID,
		SeqID:  defaultSeqID,
	})
}
