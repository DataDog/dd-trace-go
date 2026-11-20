// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package oncereset_test

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"

	"github.com/DataDog/dd-trace-go/v2/internal/lint/oncereset"
)

func TestAnalyzer(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), oncereset.Analyzer, "onceresetpkg")
}
