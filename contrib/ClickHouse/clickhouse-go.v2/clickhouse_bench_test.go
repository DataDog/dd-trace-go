// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package clickhouse

import (
	"context"
	"testing"
)

func BenchmarkExec(b *testing.B) {
	conn := Wrap(&mockConn{}, WithService("analytics"), WithHost("localhost"), WithDatabase("default"))
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		if err := conn.Exec(ctx, "SELECT 1"); err != nil {
			b.Fatal(err)
		}
	}
}
