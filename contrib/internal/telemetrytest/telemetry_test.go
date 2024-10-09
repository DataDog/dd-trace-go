// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.
package telemetrytest

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type contribPkg struct {
	ImportPath string
	Name       string
	Imports    []string
	Dir        string
}

var (
	TelemetryImport = "gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry"
	V2Import        = "github.com/DataDog/dd-trace-go/v2"
)

func (p *contribPkg) isV2Frontend() bool {
	for _, imp := range p.Imports {
		if strings.HasPrefix(imp, V2Import) {
			return true
		}
	}
	return false
}

func readPackage(t *testing.T, path string) contribPkg {
	cmd := exec.Command("go", "list", "-json", path)
	cmd.Stderr = os.Stderr
	output, err := cmd.Output()
	require.NoError(t, err)
	p := contribPkg{}
	err = json.Unmarshal(output, &p)
	require.NoError(t, err)
	return p
}

func (p *contribPkg) hasTelemetryImport(t *testing.T) bool {
	for _, imp := range p.Imports {
		if imp == TelemetryImport {
			return true
		}
	}
	// if we didn't find it imported directly, it might be imported in one of sub-package imports
	for _, imp := range p.Imports {
		if strings.HasPrefix(imp, p.ImportPath) {
			p := readPackage(t, imp)
			if p.hasTelemetryImport(t) {
				return true
			}
		}
	}
	return false
}

// TestTelemetryEnabled verifies that the expected contrib packages leverage instrumentation telemetry
func TestTelemetryEnabled(t *testing.T) {
	body, err := exec.Command("go", "list", "-json", "../../...").Output()
	require.NoError(t, err)

	var packages []contribPkg
	stream := json.NewDecoder(strings.NewReader(string(body)))
	for stream.More() {
		var out contribPkg
		err := stream.Decode(&out)
		require.NoError(t, err)
		packages = append(packages, out)
	}
	for _, pkg := range packages {
		if strings.Contains(pkg.ImportPath, "/test") || strings.Contains(pkg.ImportPath, "/internal") {
			continue
		}
		if pkg.isV2Frontend() {
			continue
		}
		if !pkg.hasTelemetryImport(t) {
			t.Fatalf(`package %q is expected use instrumentation telemetry. For more info see https://github.com/DataDog/dd-trace-go/blob/main/contrib/README.md#instrumentation-telemetry`, pkg.ImportPath)
		}
	}
}
