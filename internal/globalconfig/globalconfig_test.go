// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package globalconfig

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHeaderTag(t *testing.T) {
	SetHeaderTag("header1", "tag1")
	SetHeaderTag("header2", "tag2")

	assert.Equal(t, "tag1", cfg.headersAsTags.Get("header1"))
	assert.Equal(t, "tag2", cfg.headersAsTags.Get("header2"))
}

func TestRootSessionID_DefaultsToRuntimeID(t *testing.T) {
	assert.Equal(t, cfg.runtimeID, cfg.rootSessionID)
	assert.Equal(t, RuntimeID(), RootSessionID())
}

func TestRootSessionID_SetInProcessEnv(t *testing.T) {
	val := os.Getenv(rootSessionIDEnvVar)
	assert.Equal(t, RootSessionID(), val)
}

func TestRootSessionID_AutoPropagatedToChild(t *testing.T) {
	if os.Getenv("DD_TEST_SUBPROCESS") == "1" {
		out, _ := json.Marshal(map[string]string{
			"root_session_id": RootSessionID(),
			"runtime_id":      RuntimeID(),
		})
		os.Stderr.Write(out)
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestRootSessionID_AutoPropagatedToChild$", "-test.v")
	cmd.Env = append(os.Environ(), "DD_TEST_SUBPROCESS=1")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	require.NoError(t, err, "subprocess failed")

	var result map[string]string
	require.NoError(t, json.Unmarshal(stderr.Bytes(), &result))

	assert.Equal(t, RootSessionID(), result["root_session_id"],
		"child should inherit root session ID from parent's os.Environ automatically")
	assert.NotEqual(t, RootSessionID(), result["runtime_id"],
		"child should have its own runtime_id")
}

func TestRootSessionID_InheritedFromEnv(t *testing.T) {
	if os.Getenv("DD_TEST_SUBPROCESS") == "1" {
		out, _ := json.Marshal(map[string]string{
			"root_session_id": RootSessionID(),
			"runtime_id":      RuntimeID(),
		})
		os.Stderr.Write(out)
		return
	}

	parentRootID := "inherited-root-session-id-12345"
	cmd := exec.Command(os.Args[0], "-test.run=^TestRootSessionID_InheritedFromEnv$", "-test.v")
	cmd.Env = append(os.Environ(),
		rootSessionIDEnvVar+"="+parentRootID,
		"DD_TEST_SUBPROCESS=1",
	)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	require.NoError(t, err, "subprocess failed")

	var result map[string]string
	require.NoError(t, json.Unmarshal(stderr.Bytes(), &result))

	assert.Equal(t, parentRootID, result["root_session_id"],
		"child should inherit root session ID from parent env")
	assert.NotEqual(t, parentRootID, result["runtime_id"],
		"child should have its own runtime_id")
}

// Assert that APIs to access cfg.statsTags protect against pollution from external changes
func TestStatsTags(t *testing.T) {
	array := [6]string{"aaa", "bbb", "ccc"}
	slice1 := array[:]
	SetStatsTags(slice1)
	slice1 = append(slice1, []string{"ddd", "eee", "fff"}...)
	slice1[0] = "zzz"
	assert.Equal(t, cfg.statsTags[:3], []string{"aaa", "bbb", "ccc"})

	tags := StatsTags()
	tags[1] = "yyy"
	assert.Equal(t, cfg.statsTags[1], "bbb")
}
