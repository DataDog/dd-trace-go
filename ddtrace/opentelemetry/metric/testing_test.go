// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package metric

import (
	"os"
	"testing"

	internalconfig "github.com/DataDog/dd-trace-go/v2/internal/config"
)

func TestMain(m *testing.M) {
	internalconfig.SetUseFreshConfig(true)
	code := m.Run()
	internalconfig.SetUseFreshConfig(false)
	os.Exit(code)
}
