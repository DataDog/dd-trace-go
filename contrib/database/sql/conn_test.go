// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2019 Datadog, Inc.

package sql

import (
	"context"
	"reflect"
	"testing"
)

func TestWithSpanTags(t *testing.T) {
	type args struct {
		ctx  context.Context
		tags map[string]string
	}
	tests := []struct {
		name   string
		args   args
		wantOK bool
	}{
		{
			name: "valid get tags",
			args: args{
				ctx: context.Background(),
				tags: map[string]string{
					"tag": "value",
				},
			},
			wantOK: true,
		},
		{
			name: "invalid when context is nil",
			args: args{
				ctx: nil,
				tags: map[string]string{
					"tag": "value",
				},
			},
			wantOK: false,
		},
		{
			name: "invalid when tags is nil",
			args: args{
				ctx:  context.Background(),
				tags: nil,
			},
			wantOK: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := WithSpanTags(tt.args.ctx, tt.args.tags)
			meta, ok := ctx.Value(spanTagsKey).(map[string]string)
			if ok != tt.wantOK {
				t.Errorf("got %t, want %t", ok, tt.wantOK)
			}
			if !reflect.DeepEqual(meta, tt.args.tags) {
				t.Errorf("WithSpanTags() = %v, want %v", meta, tt.args.tags)
			}
		})
	}
}
