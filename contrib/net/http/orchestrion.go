// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

//nolint:revive
package http

// Import "./internal/orchestrion" and "./client" so that they're present in the
// dependency closure when compile-time instrumentation is used. This is
// necessary for the `orchestrion.server.yml` configuraton to be valid.
import (
	_ "github.com/DataDog/dd-trace-go/contrib/net/http/v2/client"
	_ "github.com/DataDog/dd-trace-go/contrib/net/http/v2/internal/orchestrion"
)
