// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

type contribPkg struct {
	Dir        string
	Root       string
	ImportPath string
	Name       string
}

func TestIntegrationEnabled(t *testing.T) {
	body, err := exec.Command("go", "list", "-json", "../../contrib/...").Output()
	if err != nil {
		t.Fatalf(err.Error())
	}
	var packages []contribPkg
	stream := json.NewDecoder(strings.NewReader(string(body)))
	for stream.More() {
		var out contribPkg
		err := stream.Decode(&out)
		if err != nil {
			t.Fatalf(err.Error())
		}
		packages = append(packages, out)
	}
	for _, pkg := range packages {
		if strings.Contains(pkg.ImportPath, "/test") || strings.Contains(pkg.ImportPath, "/internal") {
			continue
		}
		p := strings.Replace(pkg.Dir, pkg.Root, "../..", 1)
		body, err := exec.Command("grep", "-rl", "MarkIntegrationImported", p).Output()
		if err != nil {
			t.Fatalf(err.Error())
		}
		assert.NotEqual(t, len(body), 0, "expected %s to call MarkIntegrationImported", pkg.Name)
	}
}
