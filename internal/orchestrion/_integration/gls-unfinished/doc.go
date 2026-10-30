// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package glsunfinished shows how the goroutine-local stack (GLS) handles a span that is started
// under Orchestrion and never finished. The span stays on the GLS, so a later span that starts on
// the goroutine without a span in its context takes the open span as parent. Datadog and
// OpenTelemetry spans behave the same. contrib/ORCHESTRION.md documents this limit.
package glsunfinished

// otelc does not weave a package that has only test files. Without this file, the suite would
// skip under otelc.
