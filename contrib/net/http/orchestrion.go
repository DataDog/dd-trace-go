// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package http

// Import "./internal/orchestrion" so that it's present in the dependency
// closure when compile-time instrumentation is used. This is necessary for the
// `orchestrion.server.yml` configuraton to be valid.
import (
	_ "gopkg.in/DataDog/dd-trace-go.v1/contrib/net/http/internal/orchestrion"
)
