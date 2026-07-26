// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package metric

import (
	"testing"

	internalconfig "github.com/DataDog/dd-trace-go/v2/internal/config"
)

func setConfigEnv(t *testing.T, key, value string) {
	t.Helper()
	t.Cleanup(func() {
		internalconfig.CreateNew()
	})
	t.Setenv(key, value)
	internalconfig.CreateNew()
}
