// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package options

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

func consumeTagPair(dst map[string]string, v string) {
	values := strings.Split(v, ":")
	if len(values) != 2 {
		panic("invalid tag pair")
	}
	dst[values[0]] = values[1]
}

func TestStringSliceModify(t *testing.T) {
	t.Run("modify-original", func(t *testing.T) {
		opts := []string{"mytag:myvalue"}
		optsCopy := Copy(opts)
		opts[0] = "mytag:somethingelse"
		cfg := make(map[string]string, len(optsCopy))
		for _, v := range optsCopy {
			consumeTagPair(cfg, v)
		}
		assert.Equal(t, "myvalue", cfg["mytag"])
	})
	t.Run("modify-copy", func(t *testing.T) {
		opts := []string{"mytag:myvalue"}
		optsCopy := Copy(opts)
		optsCopy[0] = "mytag:somethingelse"
		cfg := make(map[string]string, len(opts))
		for _, v := range opts {
			consumeTagPair(cfg, v)
		}
		assert.Equal(t, "myvalue", cfg["mytag"])
	})
}

func TestGetBoolEnvValueSemanticsAndWarnings(t *testing.T) {
	const key = "DD_TRACE_128_BIT_TRACEID_LOGGING_ENABLED"
	require.True(t, parseBoolEnvValue(key, "", false, true))

	tests := []struct {
		name    string
		raw     string
		present bool
		def     bool
		want    bool
		warn    bool
	}{
		{name: "absent default false"},
		{name: "absent default true", def: true, want: true},
		{name: "valid true", raw: "1", present: true, want: true},
		{name: "valid false", raw: "false", present: true, def: true},
		{name: "invalid default false", raw: "invalid", present: true, warn: true},
		{name: "invalid default true", raw: "invalid", present: true, def: true, want: true, warn: true},
		{name: "explicit empty", present: true, def: true, want: true, warn: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			old, wasPresent := os.LookupEnv(key)
			require.NoError(t, os.Unsetenv(key))
			t.Cleanup(func() {
				if wasPresent {
					require.NoError(t, os.Setenv(key, old))
				} else {
					require.NoError(t, os.Unsetenv(key))
				}
			})
			if tt.present {
				require.NoError(t, os.Setenv(key, tt.raw))
			}
			logger := new(log.RecordLogger)
			defer log.UseLogger(logger)()

			require.Equal(t, tt.want, GetBoolEnv(key, tt.def))
			logs := strings.Join(logger.Logs(), "\n")
			if tt.warn {
				require.Contains(t, logs, "Non-boolean value for env var "+key+". Parse failed with error:")
			} else {
				require.Empty(t, logger.Logs())
			}
		})
	}
}
