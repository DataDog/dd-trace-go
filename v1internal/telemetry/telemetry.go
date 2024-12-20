// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

// Package telemetry implements a client for sending telemetry information to
// Datadog regarding usage of an APM library such as tracing or profiling.
// This package is not intended for use by external consumers, no API stability is guaranteed.
package telemetry

import "github.com/DataDog/dd-trace-go/v2/internal/telemetry"

// Namespace describes an APM product to distinguish telemetry coming from
// different products used by the same application
// This type is not intended for use by external consumers, no API stability is guaranteed.
type Namespace = telemetry.Namespace

func Count(namespace Namespace, name string, value float64, tags []string, common bool) {
	telemetry.GlobalClient.Count(namespace, name, value, tags, common)
}
