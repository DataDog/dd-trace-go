// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type contribPkg struct {
	Dir        string
	Root       string
	ImportPath string
	Name       string
}

func TestIntegrationEnabled(t *testing.T) {
	t.Skip()
	body, err := exec.Command("go", "list", "-json", "../../contrib/...").Output()
	require.NoError(t, err, "go list command failed")
	var packages []contribPkg
	stream := json.NewDecoder(strings.NewReader(string(body)))
	for stream.More() {
		var out contribPkg
		err := stream.Decode(&out)
		if err != nil {
			t.Fatal(err.Error())
		}
		packages = append(packages, out)
	}
	for _, pkg := range packages {
		if strings.Contains(pkg.ImportPath, "/test") || strings.Contains(pkg.ImportPath, "/internal") || strings.Contains(pkg.ImportPath, "/cmd") {
			continue
		}
		sep := string(os.PathSeparator)
		p := strings.Replace(pkg.Dir, pkg.Root, filepath.Join("..", ".."), 1)
		if strings.Contains(p, filepath.Join(sep, "contrib", "net", "http", "client")) || strings.Contains(p, filepath.Join(sep, "contrib", "os")) {
			continue
		}
		body, err := exec.Command("grep", "-rl", "MarkIntegrationImported", p).Output()
		if err != nil {
			t.Fatalf("%s", err.Error())
		}
		assert.NotEqual(t, len(body), 0, "expected %s to call MarkIntegrationImported", pkg.Name)
	}
}
