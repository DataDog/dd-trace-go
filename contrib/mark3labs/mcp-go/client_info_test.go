// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package mcpgo

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
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

			srv := server.NewMCPServer("test-server", "1.0.0", WithMCPServerTracing(nil))
			srv.AddTool(mcp.NewTool("echo"), func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return mcp.NewToolResultText("ok"), nil
			})
			httpServer := httptest.NewServer(server.NewStreamableHTTPServer(srv, server.WithStateLess(tc.stateless)))
			defer httpServer.Close()

			cl, err := client.NewStreamableHttpClient(httpServer.URL)
			require.NoError(t, err)
			if tc.protocolVersion != "" {
				client.WithProtocolVersion(tc.protocolVersion)(cl)
			}
			require.NoError(t, cl.Start(ctx))
			defer cl.Close()
			_, err = cl.Initialize(ctx, mcp.InitializeRequest{Params: mcp.InitializeParams{ClientInfo: mcp.Implementation{Name: "test-client", Title: "Test Client", Version: "1.0.0"}}})
			require.NoError(t, err)

			req := mcp.CallToolRequest{}
			req.Params.Name = "echo"
			_, err = cl.CallTool(ctx, req)
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
