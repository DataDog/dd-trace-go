// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package app

import "example.com/sameline/internal/log"

// F holds two distinct audited calls on the same source line, gofmt's own
// output for a short if/else body. The dedup key must keep them apart.
func F() { log.Error("first"); log.Warn("second") }
