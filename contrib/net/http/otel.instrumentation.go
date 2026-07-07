// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

//go:build tools

//nolint:revive
package http

import (
	_ "github.com/DataDog/dd-trace-go/contrib/net/http/v2/otelc"
)
