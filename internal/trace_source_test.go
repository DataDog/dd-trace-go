// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package internal

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseTraceSourceProduct(t *testing.T) {
	type args struct {
		hexStr string
	}

	var allSources = map[string]TraceSource{
		"APM": APMTraceSource,
		"ASM": ASMTraceSource,
		"DSM": DSMTraceSource,
		"DJM": DJMTraceSource,
		"DBM": DBMTraceSource,
	}

	tests := []struct {
		name        string
		args        args
		wantSources map[TraceSource]bool
		want        uint8
		wantErr     assert.ErrorAssertionFunc
	}{
		{
			name:        "empty",
			args:        args{"00"},
			wantSources: map[TraceSource]bool{},
			want:        0,
			wantErr:     assert.NoError,
		},
		{
			name:        "APM",
			args:        args{"01"},
			wantSources: map[TraceSource]bool{APMTraceSource: true},
			want:        1,
			wantErr:     assert.NoError,
		},
		{
			name:        "ASM",
			args:        args{"02"},
			wantSources: map[TraceSource]bool{ASMTraceSource: true},
			want:        2,
			wantErr:     assert.NoError,
		},
		{
			name:        "ASM-APM",
			args:        args{"03"},
			wantSources: map[TraceSource]bool{APMTraceSource: true, ASMTraceSource: true},
			want:        3,
			wantErr:     assert.NoError,
		},
		{
			name:        "DSM-DJM-DBM",
			args:        args{"1C"},
			wantSources: map[TraceSource]bool{DSMTraceSource: true, DBMTraceSource: true, DJMTraceSource: true},
			want:        28,
			wantErr:     assert.NoError,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseTraceSource(tt.args.hexStr)
			if !tt.wantErr(t, err, fmt.Sprintf("ParseTraceSourceProduct(%v)", tt.args.hexStr)) {
				return
			}

			for k, v := range allSources {
				assert.Equalf(t, tt.wantSources[v], got.IsSet(v), "Source %s should be %v", k, got.IsSet(v))
			}
			assert.EqualValuesf(t, tt.want, got, "ParseTraceSourceProduct(%v) should equal uint8 %v got value %v", tt.args.hexStr, tt.want, uint8(got))
		})
	}
}

func TestTraceSource_Set(t *testing.T) {
	type args struct {
		sources []TraceSource
	}
	tests := []struct {
		name string
		args args
		res  string
	}{
		{
			name: "empty",
			args: args{sources: nil},
			res:  "00",
		},
		{
			name: "APM",
			args: args{
				sources: []TraceSource{APMTraceSource},
			},
			res: "01",
		},
		{
			name: "ASM-twice",
			args: args{
				// Setting the same source twice does not change the underneath mask
				sources: []TraceSource{ASMTraceSource, ASMTraceSource},
			},
			res: "02",
		},
		{
			name: "APM-ASM-DBM",
			args: args{
				sources: []TraceSource{
					APMTraceSource,
					ASMTraceSource,
					DBMTraceSource,
				},
			},
			res: "13",
		},
		{
			name: "DSM-DJM",
			args: args{
				sources: []TraceSource{
					DSMTraceSource,
					DJMTraceSource,
				},
			},
			res: "0C",
		},
		{
			name: "DBM",
			args: args{
				sources: []TraceSource{DBMTraceSource},
			},
			res: "10",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := TraceSource(0)

			for _, source := range tt.args.sources {
				s.Set(source)
			}

			assert.EqualValuesf(t, tt.res, s.String(), "Set() should return %v", tt.args.sources)

		})
	}
}

func TestVerifyTraceSourceEnabled(t *testing.T) {
	type args struct {
		hexStr string
	}

	var allSources = map[string]TraceSource{
		"APM": APMTraceSource,
		"ASM": ASMTraceSource,
		"DSM": DSMTraceSource,
		"DJM": DJMTraceSource,
		"DBM": DBMTraceSource,
	}

	tests := []struct {
		name        string
		args        args
		wantSources map[TraceSource]bool
	}{
		{
			name: "empty",
			args: args{
				hexStr: "",
			},
			wantSources: map[TraceSource]bool{},
		},
		{
			name: "invalid",
			args: args{
				hexStr: "nope",
			},
			wantSources: map[TraceSource]bool{},
		},
		{
			name: "00",
			args: args{
				hexStr: "00",
			},
			wantSources: map[TraceSource]bool{},
		},
		{
			name: "01",
			args: args{
				hexStr: "01",
			},
			wantSources: map[TraceSource]bool{APMTraceSource: true},
		},
		{
			name: "02",
			args: args{
				hexStr: "02",
			},
			wantSources: map[TraceSource]bool{ASMTraceSource: true},
		},
		{
			name: "03",
			args: args{
				hexStr: "03",
			},
			wantSources: map[TraceSource]bool{APMTraceSource: true, ASMTraceSource: true},
		},
		{
			name: "04",
			args: args{
				hexStr: "04",
			},
			wantSources: map[TraceSource]bool{DSMTraceSource: true},
		},
		{
			name: "05",
			args: args{
				hexStr: "05",
			},
			wantSources: map[TraceSource]bool{APMTraceSource: true, DSMTraceSource: true},
		},
		{
			name: "08",
			args: args{
				hexStr: "08",
			},
			wantSources: map[TraceSource]bool{DJMTraceSource: true},
		},
		{
			name: "0C",
			args: args{
				hexStr: "0C",
			},
			wantSources: map[TraceSource]bool{DSMTraceSource: true, DJMTraceSource: true},
		},
		{
			name: "10",
			args: args{
				hexStr: "10",
			},
			wantSources: map[TraceSource]bool{DBMTraceSource: true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for k, v := range allSources {
				assert.Equalf(t, tt.wantSources[v], VerifyTraceSourceEnabled(tt.args.hexStr, v), "Source %s should be %v for mask %s",
					k, tt.wantSources[v], tt.args.hexStr)
			}
		})
	}
}
