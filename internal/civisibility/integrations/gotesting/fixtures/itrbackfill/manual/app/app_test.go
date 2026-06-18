// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package app

import (
	"testing"

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations/gotesting/fixtures/itrbackfill/manual/lib"
)

func TestCoversLib(t *testing.T) {
	if lib.Answer() != 42 {
		t.Fatal("unexpected answer")
	}
}

func TestRunsNormally(t *testing.T) {
	t.Log("normal test")
}
