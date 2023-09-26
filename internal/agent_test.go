// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package internal

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAgentURLFromEnv2(t *testing.T) {
	for name, tc := range map[string]struct {
		input string
		want  string
	}{
		"empty":    {input: "", want: ""},
		"protocol": {input: "bad://custom:1234", want: ""},
		"invalid":  {input: "http://localhost%+o:8126", want: ""},
		"http":     {input: "http://custom:1234", want: "http://custom:1234"},
		"https":    {input: "https://custom:1234", want: "https://custom:1234"},
		"unix":     {input: "unix:///path/to/custom.socket", want: "unix:///path/to/custom.socket"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("DD_TRACE_AGENT_URL", tc.input)
			url := AgentURLFromEnv()
			if tc.want == "" {
				assert.Nil(t, url)
			} else {
				assert.Equal(t, tc.want, url.String())
			}
		})
	}
}
