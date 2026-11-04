// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package dyngo

// The GLS rules in gls.otelc.yaml and internal/orchestrion/gls.orchestrion.yml
// add this import to operation.go. Importing it here too keeps it in the
// compile's importcfg, so otelc never resolves it with a nested `go list
// -export`. When dyngo is itself compiled inside another nested build, that
// resolution runs uninstrumented and caches a dyngo the application cannot
// link against ("fingerprint mismatch" for internal/orchestrion).
import _ "github.com/DataDog/dd-trace-go/v2/internal/orchestrion"
