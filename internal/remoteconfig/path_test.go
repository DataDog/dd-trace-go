// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package remoteconfig

import (
	"math/rand"
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func TestParsePath(t *testing.T) {
	type testCase struct {
		Filename string
		Expected Path
		Valid    bool
	}
	testCases := map[string]testCase{
		"LIVE_DEBUGGING": {
			Filename: "datadog/2/LIVE_DEBUGGING/9e413cda-647b-335b-adcd-7ce453fc2284/config",
			Expected: Path{
				Source:   DatadogSource{OrgID: "2"},
				Product:  "LIVE_DEBUGGING",
				ConfigID: "9e413cda-647b-335b-adcd-7ce453fc2284",
				Name:     "config",
			},
			Valid: true,
		},
		"ASM_DD": {
			Filename: "datadog/2/ASM_DATA/blocked_ips/77b1c2865da79341f835e040b0e8a015c74672e4e906430d320408af44742be9",
			Expected: Path{
				Source:   DatadogSource{OrgID: "2"},
				Product:  "ASM_DATA",
				ConfigID: "blocked_ips",
				Name:     "77b1c2865da79341f835e040b0e8a015c74672e4e906430d320408af44742be9",
			},
			Valid: true,
		},
		"employee": {
			Filename: "employee/ASM_DD/13.recommended.json/config",
			Expected: Path{
				Source:   EmployeeSource{},
				Product:  "ASM_DD",
				ConfigID: "13.recommended.json",
				Name:     "config",
			},
			Valid: true,
		},
		"random": {
			Filename: "random",
			Valid:    false,
		},
		"missing_parts": {
			Filename: "datadog/2/ASM_DATA/blocked_ips",
			Valid:    false,
		},
		"blank_product": {
			Filename: "datadog/2//blocked_ips/77b1c2865da79341f835e040b0e8a015c74672e4e906430d320408af44742be9",
			Valid:    false,
		},
		"blank_config_id": {
			Filename: "datadog/2/ASM_DATA//77b1c2865da79341f835e040b0e8a015c74672e4e906430d320408af44742be9",
			Valid:    false,
		},
		"blank_name": {
			Filename: "datadog/2/ASM_DATA/blocked_ips/",
			Valid:    false,
		},
		"too_many_parts": {
			Filename: "datadog/2/ASM_DATA/blocked_ips/77b1c2865da79341f835e040b0e8a015c74672e4e906430d320408af44742be9/extraneous",
			Valid:    false,
		},
		"invalid_source": {
			Filename: "invalid/ASM_DATA/blocked_ips/77b1c2865da79341f835e040b0e8a015c74672e4e906430d320408af44742be9",
			Valid:    false,
		},
		"invalid_org_id": {
			Filename: "datadog/1337.42/ASM_DATA/blocked_ips/77b1c2865da79341f835e040b0e8a015c74672e4e906430d320408af44742be9",
			Valid:    false,
		},
		"blank_org_id": {
			Filename: "datadog//ASM_DATA/blocked_ips/77b1c2865da79341f835e040b0e8a015c74672e4e906430d320408af44742be9",
			Valid:    false,
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			path, ok := ParsePath(tc.Filename)
			if tc.Valid {
				assert.True(t, ok)
				assert.Equal(t, tc.Expected, path)
				assert.Equal(t, tc.Filename, path.String())
				assert.True(t, strings.HasPrefix(tc.Filename, path.Source.String()+"/"),
					"filename should start with source name")
			} else {
				assert.False(t, ok)
				assert.Zero(t, path)
			}
		})
	}
}

var (
	benchPath Path
	benchOk   bool
)

// BenchmarkParsePath compares the current implementation (based on [strings.Split]) with a
// [regexp.Regexp] based implementation that uses the regular expression pattern from the remote
// config documentation. This confirms the current implementation is about 8x faster than one that
// uses [regexp.Regexp].
func BenchmarkParsePath(b *testing.B) {
	var samples []string
	for _, source := range []string{"employee", "datadog/2", "datadog/1337"} {
		for _, product := range []string{"ASM_DD", "LIVE_DEBUGGING", "OTHER"} {
			for range 20 {
				configID := uuid.NewString()
				samples = append(samples, source+"/"+product+"/"+configID+"/config")
			}
		}
	}
	rand.Shuffle(len(samples), func(i, j int) { samples[i], samples[j] = samples[j], samples[i] })

	b.Run("split", func(b *testing.B) {
		for i := range b.N {
			benchPath, benchOk = ParsePath(samples[i%len(samples)])
		}
	})

	re := regexp.MustCompile(`^(datadog/\d+|employee)/([^/]+)/([^/]+)/([^/]+)$`)
	b.Run("regexp", func(b *testing.B) {
		for i := range b.N {
			parts := re.FindStringSubmatch(samples[i%len(samples)])
			benchOk = parts != nil
			benchPath = Path{
				Source:   literalSource{literal: parts[0]},
				Product:  parts[2],
				ConfigID: parts[3],
				Name:     parts[4],
			}
		}
	})
}

type literalSource struct {
	source
	literal string
}

func (s literalSource) String() string {
	return s.literal
}
