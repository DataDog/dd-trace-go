// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package namingschema

import (
	"fmt"
	"os"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPackageInitUsesNamingEnvironment(t *testing.T) {
	if os.Getenv("DD_NAMING_SCHEMA_INIT_HELPER") == "1" {
		cfg := GetConfig()
		fmt.Printf("%d,%t", cfg.NamingSchemaVersion, cfg.RemoveFakeServiceNames)
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestPackageInitUsesNamingEnvironment$")
	cmd.Env = append(withoutEnv(os.Environ(),
		"DD_NAMING_SCHEMA_INIT_HELPER",
		"DD_TRACE_SPAN_ATTRIBUTE_SCHEMA",
		"DD_TRACE_REMOVE_INTEGRATION_SERVICE_NAMES_ENABLED",
	),
		"DD_NAMING_SCHEMA_INIT_HELPER=1",
		"DD_TRACE_SPAN_ATTRIBUTE_SCHEMA=v1",
		"DD_TRACE_REMOVE_INTEGRATION_SERVICE_NAMES_ENABLED=true",
	)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
	require.Contains(t, string(out), "1,true")
}

func withoutEnv(environment []string, keys ...string) []string {
	filtered := make([]string, 0, len(environment))
	for _, entry := range environment {
		keep := true
		for _, key := range keys {
			if len(entry) > len(key) && entry[:len(key)+1] == key+"=" {
				keep = false
				break
			}
		}
		if keep {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}
