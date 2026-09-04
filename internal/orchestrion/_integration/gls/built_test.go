// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package gls

import (
	"github.com/DataDog/orchestrion/runtime/built"

	"github.com/DataDog/dd-trace-go/v2/internal/otelc"
)

// glsWoven reports whether this build has the goroutine-local storage woven in,
// by either orchestrion or otelc. Both inject the same runtime.g field, the same
// pair of linknamed accessors, and the same span lifecycle calls, so the tests in
// this package must hold identically under either one.
var glsWoven = built.WithOrchestrion || otelc.Enabled()
