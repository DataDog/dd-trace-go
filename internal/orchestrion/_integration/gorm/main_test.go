// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package gorm

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/x/agenttest"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/maintest"
)

// TestOpenInMain covers gorm.Open called from package main, in a package-level
// var and under an import alias. Each query must give exactly one gorm span
// with one database/sql child.
func TestOpenInMain(t *testing.T) {
	agent := maintest.Run(t, "./gorm/mainapp")
	spans := agent.Agent.(agenttest.SpanLister).Spans()
	for _, resource := range []string{"SELECT 'var'", "SELECT 'main'", "SELECT 'alias'"} {
		var gormSpans, sqlSpans []*agenttest.Span
		for _, s := range spans {
			if s.Resource != resource {
				continue
			}
			switch s.Meta["component"] {
			case "gorm.io/gorm.v1":
				gormSpans = append(gormSpans, s)
			case "database/sql":
				sqlSpans = append(sqlSpans, s)
			}
		}
		require.Lenf(t, gormSpans, 1, "gorm spans for %q", resource)
		require.Lenf(t, sqlSpans, 1, "database/sql spans for %q", resource)
		require.Equal(t, "gorm.row_query", gormSpans[0].Operation)
		require.Equal(t, gormSpans[0].SpanID, sqlSpans[0].ParentID)
	}
}
