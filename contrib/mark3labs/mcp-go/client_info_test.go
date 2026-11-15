// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package mcpgo

import (
	"context"
	"net/http"
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

func newStatelessClientInfoTestServer() *httptest.Server {
	srv := server.NewMCPServer("test-server", "1.0.0", WithMCPServerTracing(nil))
	srv.AddTool(mcp.NewTool("echo"), func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("ok"), nil
	})
	return httptest.NewServer(server.NewStreamableHTTPServer(srv, server.WithStateLess(true)))
}

// mcp-go v0.42 records the handshake on stdio and SSE sessions. Streamable HTTP sessions
// record it from v1.
func TestIntegrationToolCallClientInfoFromSession(t *testing.T) {
	tt := testTracer(t)
	ctx := context.Background()
	srv := server.NewMCPServer("test-server", "1.0.0", WithMCPServerTracing(nil))
	srv.AddTool(mcp.NewTool("echo"), func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("ok"), nil
	})

	httpServer := httptest.NewServer(server.NewSSEServer(srv))
	defer httpServer.Close()

	cl, err := client.NewSSEMCPClient(httpServer.URL + "/sse")
	require.NoError(t, err)
	require.NoError(t, cl.Start(ctx))
	defer cl.Close()
	_, err = cl.Initialize(ctx, mcp.InitializeRequest{Params: mcp.InitializeParams{ClientInfo: mcp.Implementation{Name: "test-client", Version: "1.0.0"}}})
	require.NoError(t, err)
	req := mcp.CallToolRequest{}
	req.Params.Name = "echo"
	_, err = cl.CallTool(ctx, req)
	require.NoError(t, err)

	tracer.Flush()
	want := []string{"mcp_client_name:test-client", "mcp_client_version:1.0.0"}
	// Newer SDK clients skip the handshake and send the client info in _meta instead.
	if initSpan := tt.FindSpan("mcp.initialize"); initSpan != nil {
		assertClientInfoTags(t, initSpan.Tags, want)
	}
	assertClientInfoTags(t, tt.RequireSpan(t, "echo").Tags, want)
}

func TestIntegrationStatelessToolCallClientInfo(t *testing.T) {
	for _, tc := range []struct {
		name   string
		params string
		want   []string
	}{
		{
			name:   "from request meta",
			params: `{"name":"echo","arguments":{},"_meta":{"io.modelcontextprotocol/clientInfo":{"name":"meta-client","title":"Meta Client","version":"1.2.3"}}}`,
			want:   []string{"mcp_client_name:meta-client", "mcp_client_title:Meta Client", "mcp_client_version:1.2.3"},
		},
		// A stateless server keeps no handshake, so a request without client info gets no tags.
		{name: "without client info", params: `{"name":"echo","arguments":{}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tt := testTracer(t)
			httpServer := newStatelessClientInfoTestServer()
			defer httpServer.Close()

			req, err := http.NewRequest(http.MethodPost, httpServer.URL, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":`+tc.params+`}`))
			require.NoError(t, err)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Accept", "application/json, text/event-stream")
			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			defer resp.Body.Close()
			require.Equal(t, http.StatusOK, resp.StatusCode)

			tracer.Flush()
			assertClientInfoTags(t, tt.RequireSpan(t, "echo").Tags, tc.want)
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
