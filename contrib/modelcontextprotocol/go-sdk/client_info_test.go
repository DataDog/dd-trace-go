// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gosdk

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

func TestIntegrationToolCallClientInfo(t *testing.T) {
	clientTags := []string{"mcp_client_name:test-client", "mcp_client_title:Test Client", "mcp_client_version:1.0.0"}
	for _, tc := range []struct {
		name            string
		stateless       bool
		protocolVersion string
		wantInitialize  bool
		wantToolTags    []string
	}{
		{name: "stateful legacy", protocolVersion: "2025-11-25", wantInitialize: true, wantToolTags: clientTags},
		{name: "stateless 2026-07-28", stateless: true, wantToolTags: clientTags},
		// A stateless server keeps no handshake, and legacy requests do not repeat the client info.
		{name: "stateless legacy", stateless: true, protocolVersion: "2025-11-25", wantInitialize: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tt := testTracer(t)
			ctx := context.Background()

			server := mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "1.0.0"}, nil)
			AddTracing(server)
			server.AddTool(&mcp.Tool{Name: "echo", InputSchema: map[string]any{"type": "object"}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, nil
			})
			handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: tc.stateless})
			httpServer := httptest.NewServer(handler)
			defer httpServer.Close()

			client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Title: "Test Client", Version: "1.0.0"}, nil)
			session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: httpServer.URL}, &mcp.ClientSessionOptions{ProtocolVersion: tc.protocolVersion})
			require.NoError(t, err)
			defer session.Close()

			_, err = session.CallTool(ctx, &mcp.CallToolParams{Name: "echo", Arguments: map[string]any{}})
			require.NoError(t, err)

			tracer.Flush()
			if tc.wantInitialize {
				assertClientInfoTags(t, tt.RequireSpan(t, "mcp.initialize").Tags, clientTags)
			}
			assertClientInfoTags(t, tt.RequireSpan(t, "echo").Tags, tc.wantToolTags)
		})
	}
}

func assertClientInfoTags(t *testing.T, tags, want []string) {
	t.Helper()
	var got []string
	for _, tag := range tags {
		if strings.HasPrefix(tag, "mcp_client_") {
			got = append(got, tag)
		}
	}
	assert.ElementsMatch(t, want, got)
}
