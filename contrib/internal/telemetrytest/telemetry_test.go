package telemetrytest

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"gopkg.in/DataDog/dd-trace-go.v1/contrib/gorilla/mux"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry"
)

func TestIntegration(t *testing.T) {
	mux.NewRouter()
	integrations := telemetry.Integrations()
	assert.Len(t, integrations, 1)
	assert.Equal(t, integrations[0].Name, "gorilla/mux")
	assert.True(t, integrations[0].Enabled)
}

var TELEMETRY_IMPORT = "gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry"

type contribPkg struct {
	ImportPath string
	Name       string
	Imports    []string
}

func (p *contribPkg) hasImport(imp string) bool {
	for _, imp := range p.Imports {
		if imp == TELEMETRY_IMPORT {
			return true
		}
	}
	return false
}

// TestTelemetryInit verifies that the expected contrib packages leverage instrumentation telemetry
func TestTelemetryInit(t *testing.T) {
	tracked := map[string]struct{}{"mux": {}}
	cmd := "go list -json=ImportPath,Name,Imports  ../.././..."
	body, err := exec.Command("bash", "-c", cmd).Output()
	if err != nil {
		t.Fatalf(err.Error())
	}
	var packages []contribPkg
	formatted := fmt.Sprintf("[%s]", strings.TrimRight(strings.Replace(string(body), "}", "},", -1), ",\n"))
	err = json.Unmarshal([]byte(formatted), &packages)
	if err != nil {
		t.Fatalf(err.Error())
	}
	for _, pkg := range packages {
		if strings.Contains(pkg.ImportPath, "/test") || strings.Contains(pkg.ImportPath, "/internal") {
			continue
		}
		if _, ok := tracked[pkg.Name]; ok {
			if !pkg.hasImport(TELEMETRY_IMPORT) {
				t.Fatalf(`package '%s' is expected use instrumentation telemetry.
			Please reference other contrib packages or the 'internal/telemetry' package 
			(https://github.com/DataDog/dd-trace-go/tree/main/internal/telemetry) 
			on how to leverage instrumentation telemetry in a contrib package`, pkg.Name)
			}
		}
	}
}
