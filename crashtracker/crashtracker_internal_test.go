// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package crashtracker

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveServiceFallsBackToExecutableName(t *testing.T) {
	// Neither globalconfig.SetServiceName (the tracer's job, not this
	// package's) nor DD_SERVICE has run/been set in this test binary, so this
	// exercises the final fallback.
	t.Setenv("DD_SERVICE", "")

	got := resolveService()
	want := filepath.Base(os.Args[0])
	if got != want {
		t.Errorf("resolveService() = %q, want %q (filepath.Base(os.Args[0]), matching tracer.Start's own default)", got, want)
	}
}
