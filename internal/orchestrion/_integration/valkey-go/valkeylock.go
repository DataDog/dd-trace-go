// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux || !githubci

package valkey

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/valkey-io/valkey-go"
	"github.com/valkey-io/valkey-go/valkeylock"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCaseValkeylock checks that the client valkeylock builds with
// valkey.NewClient is traced.
type TestCaseValkeylock struct {
	valkeyServer
	locker valkeylock.Locker
}

func (tc *TestCaseValkeylock) Setup(_ context.Context, t *testing.T) {
	var err error
	tc.locker, err = valkeylock.NewLocker(valkeylock.LockerOption{
		ClientOption: valkey.ClientOption{InitAddress: []string{tc.addr}, DisableCache: true},
		KeyMajority:  1,
	})
	require.NoError(t, err)
	t.Cleanup(tc.locker.Close)
}

func (tc *TestCaseValkeylock) Run(ctx context.Context, t *testing.T) {
	span, ctx := tracer.StartSpanFromContext(ctx, "test.root")
	defer span.Finish()

	client := tc.locker.Client()
	require.NoError(t, client.Do(ctx, client.B().Set().Key("valkeylock").Value("value").Build()).Error())
}

func (*TestCaseValkeylock) ExpectedSpanCount() int {
	return 2
}

func (*TestCaseValkeylock) ExpectedTraces() trace.Traces {
	return trace.Traces{
		{
			Tags:     map[string]any{"name": "test.root"},
			Children: trace.Traces{commandSpan("valkey.client", "SET")},
		},
	}
}
