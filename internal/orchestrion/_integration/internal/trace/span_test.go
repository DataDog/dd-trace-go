// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

package trace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/xlab/treeprint"
	"gotest.tools/v3/golden"
)

var testdata string

func TestMatchesAny(t *testing.T) {
	cases, err := os.ReadDir(testdata)
	require.NoError(t, err)
	for _, caseDir := range cases {
		if !caseDir.IsDir() {
			continue
		}

		name := caseDir.Name()
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var (
				expected *Trace
				actual   []*Trace
			)
			{
				data, err := os.ReadFile(filepath.Join(testdata, name, "expected.json"))
				require.NoError(t, err)
				require.NoError(t, json.Unmarshal(data, &expected))
			}
			{
				data, err := os.ReadFile(filepath.Join(testdata, name, "actual.json"))
				require.NoError(t, err)
				require.NoError(t, json.Unmarshal(data, &actual))
			}

			matches, diff := expected.matchesAny(actual, treeprint.NewWithRoot("Root"))
			goldFile := filepath.Join(name, "diff.txt")
			if matches != nil {
				golden.Assert(t, "<none>", goldFile)
				require.Empty(t, diff, 0)
			} else {
				require.NotEmpty(t, diff)
				golden.Assert(t, strings.TrimSpace(diff.String()), goldFile)
			}
		})
	}
}

func init() {
	_, file, _, _ := runtime.Caller(0)
	testdata = filepath.Join(file, "..", "testdata")
}
