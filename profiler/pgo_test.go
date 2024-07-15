// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package profiler

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/pprof"
	"strings"
	"testing"
	"text/template"

	"github.com/stretchr/testify/require"
)

// Test_pgoTag verifies that the pgoTag function returns the expected value
// depending on whether the binary was built with PGO enabled or not.
func Test_pgoTag(t *testing.T) {
	var h pgoTestHelper
	mainSrc := h.mainSource(t)
	require.Equal(t, "pgo:false", h.goRun(t, mainSrc, nil))
	require.Equal(t, "pgo:true", h.goRun(t, mainSrc, h.cpuProfile(t)))
}

// pgoTestHelper is used to group together test helper functions for Test_pgoTag.
type pgoTestHelper struct{}

// cpuProfile returns a valid CPU profile. No attempt is made to populate it
// with data.
func (pgoTestHelper) cpuProfile(t *testing.T) []byte {
	var buf bytes.Buffer
	require.NoError(t, pprof.StartCPUProfile(&buf))
	pprof.StopCPUProfile()
	data := buf.Bytes()
	require.NotZero(t, len(data))
	return data
}

// mainSource returns the source code for a main package that prints the result
// of calling pgoTag.
func (pgoTestHelper) mainSource(t *testing.T) string {
	// The code below extracts the source of the pgoTag function from the source
	// of this package and uses it to generate a main package that prints the
	// result of calling it. This is a bit hacky, but I think it strives a
	// reasonable balance compared to the alternatives I see:
	//
	// a) Not having a test: I considered this, but I want to test this since
	//    the "-pgo" debug info is not officially documented right now.
	// b) Full integration test importing dd-trace-go from main.go: This seemed
	//    like it would be a lot more code and complexity.

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "pgo.go", nil, 0)
	require.NoError(t, err)
	var funcs bytes.Buffer
	for _, d := range file.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || (fd.Name.Name != "pgoEnabled" && fd.Name.Name != "pgoTag") {
			continue
		}
		printer.Fprint(&funcs, fset, fd)
		fmt.Fprintln(&funcs, ``)
	}

	var out bytes.Buffer
	template.Must(template.New("profiler").Parse(`
package main

import (
	"fmt"
	"runtime/debug"
)

func main() {
	fmt.Println(pgoTag())
}

{{.}}
`)).Execute(&out, funcs.String())
	return out.String()
}

// goRun runs the given source code with the given CPU profile, if any, and
// returns the trimmed output.
func (pgoTestHelper) goRun(t *testing.T, src string, cpuProfile []byte) string {
	// write main.go and default.pgo
	tmpDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte(src), 0644))
	if cpuProfile != nil {
		require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "default.pgo"), cpuProfile, 0644))
	}

	// go mod init
	goMod := exec.Command("go", "mod", "init", "pgo_tag")
	goMod.Dir = tmpDir
	require.NoError(t, goMod.Run())

	// go run
	var out bytes.Buffer
	cmd := exec.Command("go", "run", ".")
	cmd.Stdout = &out
	cmd.Stderr = &out
	cmd.Dir = tmpDir
	require.NoError(t, cmd.Run(), "out=%s", out.String())
	return strings.TrimSpace(out.String())
}
