// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package clickhouse

import (
	"io"
	"os"
	"testing"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

func TestIntegration(t *testing.T) {
	if _, ok := os.LookupEnv("INTEGRATION"); !ok {
		t.Skip("set INTEGRATION to run against the ClickHouse docker-compose service")
	}
	for _, protocol := range []clickhouse.Protocol{clickhouse.Native, clickhouse.HTTP} {
		t.Run(protocol.String(), func(t *testing.T) {
			addr := "localhost:9000"
			if protocol == clickhouse.HTTP {
				addr = "localhost:8123"
			}
			raw, err := clickhouse.Open(&clickhouse.Options{Addr: []string{addr}, Protocol: protocol})
			require.NoError(t, err)
			t.Cleanup(func() { assert.NoError(t, raw.Close()) })
			mt := mocktracer.Start()
			defer mt.Stop()
			parent, ctx := tracer.StartSpanFromContext(t.Context(), "request")
			defer parent.Finish()
			conn := Wrap(raw)
			require.NoError(t, conn.Ping(ctx))
			var value uint8
			require.NoError(t, conn.QueryRow(ctx, "SELECT toUInt8(?)", 42).Scan(&value))
			assert.Equal(t, uint8(42), value)
			rows, err := conn.Query(ctx, "SELECT toUInt8(7)")
			require.NoError(t, err)
			require.True(t, rows.Next())
			require.NoError(t, rows.Scan(&value))
			assert.Equal(t, uint8(7), value)
			require.NoError(t, rows.Close())
			var values []struct {
				Value uint8 `ch:"value"`
			}
			require.NoError(t, conn.Select(ctx, &values, "SELECT toUInt8(9) AS value"))
			require.Len(t, values, 1)
			assert.Equal(t, uint8(9), values[0].Value)
			require.NoError(t, conn.Exec(ctx, "SELECT 1"))
			require.Error(t, conn.Exec(ctx, "SELECT unknown_column"))
			spans := mt.FinishedSpans()
			require.Len(t, spans, 6)
			for _, span := range spans {
				assert.Equal(t, parent.Context().SpanID(), span.ParentID())
				assert.Equal(t, OperationName, span.OperationName())
				assert.Equal(t, DBSystemName, span.Tag(ext.DBSystem))
			}
			assert.NotNil(t, spans[5].Tag(ext.ErrorMsg))
			if protocol == clickhouse.HTTP {
				reader, err := conn.QueryFormat(ctx, "CSV", "SELECT toUInt8(11)")
				require.NoError(t, err)
				data, err := io.ReadAll(reader)
				require.NoError(t, err)
				require.NoError(t, reader.Close())
				assert.Equal(t, "11\n", string(data))
				require.Len(t, mt.FinishedSpans(), 7)
			}
		})
	}
}
