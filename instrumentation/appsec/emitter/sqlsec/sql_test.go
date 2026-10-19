// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package sqlsec

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/appsec/events"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/dyngo"
)

func TestSQLMonitoringScope(t *testing.T) {
	parent := dyngo.NewRootOperation()
	ctx := dyngo.RegisterOperation(context.Background(), parent)
	var received []SQLOperationArgs
	dyngo.On(parent, func(op *SQLOperation, args SQLOperationArgs) {
		received = append(received, args)
		if !args.MonitorOnly {
			dyngo.EmitData(op, &events.BlockingSecurityEvent{})
		}
	})
	MonitorSQLOperation(context.Background(), "SELECT 1", "postgresql")
	require.Empty(t, received)
	MonitorSQLOperation(ctx, "SELECT 1", "postgresql")
	require.Equal(t, []SQLOperationArgs{{Query: "SELECT 1", Driver: "postgresql", MonitorOnly: true}}, received)

	// A marker applies to the nested driver call, including when DBM adds comments.
	marked := WithSQLOperationChecked(ctx)
	MonitorSQLOperation(marked, "/* dbm */ SELECT 1", "postgresql")
	require.Len(t, received, 1)
	MonitorSQLOperation(ctx, "SELECT 1", "postgresql")
	require.Len(t, received, 2, "repeated SQL in the original context must still be monitored")
	require.True(t, events.IsSecurityError(ProtectSQLOperation(marked, "SELECT 1", "postgresql")))
	require.False(t, received[2].MonitorOnly, "monitoring suppression must not disable protection")
}

func TestWithSQLOperationCheckedWithoutParent(t *testing.T) {
	ctx := context.Background()
	require.Equal(t, ctx, WithSQLOperationChecked(ctx))
	require.Zero(t, testing.AllocsPerRun(100, func() {
		WithSQLOperationChecked(ctx)
	}))
}

func BenchmarkMonitorSQLOperation(b *testing.B) {
	parent := dyngo.NewRootOperation()
	ctx := dyngo.RegisterOperation(context.Background(), parent)
	for name, ctx := range map[string]context.Context{
		"native":          ctx,
		"already-checked": WithSQLOperationChecked(ctx),
	} {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				MonitorSQLOperation(ctx, "SELECT 1", "postgresql")
			}
		})
	}
}
