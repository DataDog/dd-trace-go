// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.
package telemetrytest

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"gopkg.in/DataDog/dd-trace-go.v1/contrib/gorilla/mux"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry"
)

// TestIntegrationInfo verifies that an integration leveraging instrumentation telemetry
// sends the correct data to the telemetry client.
func TestIntegrationInfo(t *testing.T) {
	mux.NewRouter()
	integrations := telemetry.Integrations()
	assert.Len(t, integrations, 1)
	assert.Equal(t, integrations[0].Name, "gorilla/mux")
	assert.True(t, integrations[0].Enabled)
}

type contribPkg struct {
	ImportPath string
	Name       string
	Imports    []string
}

var TelemetryImport = "gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry"

func (p *contribPkg) hasTelemetryImport() bool {
	for _, imp := range p.Imports {
		if imp == TelemetryImport {
			return true
		}
	}
	return false
}

// parseContribPath takes the current path and returns the relative path
// to the contrib folder. The current path must be a sub-directory of the
// contrib folder or the parent dd-trace-go directory.
func parseContribPath(path string) (string, error) {
	if filepath.Base(path) == "dd-trace-go" {
		return "./contrib", nil
	}
	dirs := strings.Split(path, "/")
	for i, dir := range dirs {
		if dir == "contrib" {
			contribPath := filepath.Join(dirs[:i+1]...)
			return contribPath, nil
			// rel, err := filepath.Rel(strings.TrimPrefix(path, "/"), contribPath)
			// if err != nil {
			// 	return "", err
			// }
			// return rel, nil
		}
	}
	return "", fmt.Errorf("contrib was not found as a parent folder and current working directory is not dd-trace-go")
}

// TestTelemetryEnabled verifies that the expected contrib packages leverage instrumentation telemetry
func TestTelemetryEnabled(t *testing.T) {
	tracked := map[string]struct{}{"mux": {}}

	pkgInfo, err := os.Open("packageInfo.txt")
	if err != nil {
		t.Fatalf(err.Error())
	}
	defer pkgInfo.Close()

	byteValue, _ := ioutil.ReadAll(pkgInfo)

	// path, err := os.Getwd()
	// if err != nil {
	// 	t.Fatalf(err.Error())
	// }
	// path, err = parseContribPath(path)
	// if err != nil {
	// 	t.Fatalf(err.Error())
	// }
	// path = fmt.Sprintf("%s%s", path, "/...")
	// jsonFlags := "-json=ImportPath,Name,Imports"
	// body, err := exec.Command("go", "list", jsonFlags, path).Output()
	// if err != nil {
	// 	t.Fatalf(err.Error())
	// }
	var packages []contribPkg
	stream := json.NewDecoder(strings.NewReader(string(byteValue)))
	for stream.More() {
		var out contribPkg
		err := stream.Decode(&out)
		if err != nil {
			t.Fatalf(err.Error())
		}
		packages = append(packages, out)
	}
	// need to reformat the output of the go list command to be a valid json
	for _, pkg := range packages {
		if strings.Contains(pkg.ImportPath, "/test") || strings.Contains(pkg.ImportPath, "/internal") {
			continue
		}
		if _, ok := tracked[pkg.Name]; ok {
			if !pkg.hasTelemetryImport() {
				t.Fatalf(`package '%s' is expected use instrumentation telemetry. For more info see https://github.com/DataDog/dd-trace-go/blob/main/contrib/README.md`, pkg.Name)
			}
		}
	}
}
