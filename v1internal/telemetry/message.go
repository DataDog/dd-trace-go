// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

// This package is not intended for use by external consumers, no API stability is guaranteed.
package telemetry

import "github.com/DataDog/dd-trace-go/v2/internal/telemetry"

// Integration is an integration that is configured to be traced automatically.
// This type is not intended for use by external consumers, no API stability is guaranteed.
type Integration = telemetry.Integration

// Configuration is a library-specific configuration value
// that should be initialized through StringConfig, IntConfig, FloatConfig, or BoolConfig
// This type is not intended for use by external consumers, no API stability is guaranteed.
type Configuration = telemetry.Configuration
