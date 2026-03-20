// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package os

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/internal/globalconfig"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInjectSessionEnv_NilEnv(t *testing.T) {
	cmd := exec.Command("echo", "hello")
	assert.Nil(t, cmd.Env)

	InjectSessionEnv(cmd)

	require.NotNil(t, cmd.Env)
	found := findEnv(cmd.Env, "DD_ROOT_GO_SESSION_ID")
	assert.Equal(t, globalconfig.RootSessionID(), found)
}

func TestInjectSessionEnv_ExistingEnv(t *testing.T) {
	cmd := exec.Command("echo", "hello")
	cmd.Env = []string{"FOO=bar"}

	InjectSessionEnv(cmd)

	assert.Contains(t, cmd.Env, "FOO=bar")
	found := findEnv(cmd.Env, "DD_ROOT_GO_SESSION_ID")
	assert.Equal(t, globalconfig.RootSessionID(), found)
}

func TestInjectSessionEnv_ChildInheritsRootSessionID(t *testing.T) {
	cmd := exec.Command("env")
	InjectSessionEnv(cmd)

	out, err := cmd.Output()
	require.NoError(t, err)

	var found bool
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "DD_ROOT_GO_SESSION_ID=") {
			assert.Equal(t, "DD_ROOT_GO_SESSION_ID="+globalconfig.RootSessionID(), line)
			found = true
			break
		}
	}
	assert.True(t, found, "DD_ROOT_GO_SESSION_ID should be in child's environment")
}

func findEnv(env []string, key string) string {
	prefix := key + "="
	for _, e := range env {
		if strings.HasPrefix(e, prefix) {
			return e[len(prefix):]
		}
	}
	return ""
}
