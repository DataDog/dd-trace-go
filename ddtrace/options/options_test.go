// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package options

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
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
