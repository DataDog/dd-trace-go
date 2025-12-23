// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

//go:build noprotobuf

package tracer

import (
	"time"

	"github.com/DataDog/datadog-agent/pkg/obfuscate"
)

var (
	defaultStatsBucketSize = 10
	withCurrentBucket      = true
)

type concentrator struct {
	In chan any
}

func newConcentrator(_ any, _ int, _ any) *concentrator {
	return nil
}

func (*concentrator) Start() {
}

func (*concentrator) Stop() {
}

func (*concentrator) newTracerStatSpan(_ *Span, _ *obfuscate.Obfuscator) (any, bool) {
	return nil, false
}

func (*concentrator) flushAndSend(_ time.Time, _ any) {
}
