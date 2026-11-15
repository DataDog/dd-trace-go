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

func newClientInfoTestHandler(stateless bool) http.Handler {
	server := mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "1.0.0"}, nil)
	AddTracing(server)
	server.AddTool(&mcp.Tool{Name: "echo", InputSchema: map[string]any{"type": "object"}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, nil
	})
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: stateless, JSONResponse: true})
}

func TestIntegrationToolCallClientInfoFromSession(t *testing.T) {
	tt := testTracer(t)
	ctx := context.Background()
	httpServer := httptest.NewServer(newClientInfoTestHandler(false))
	defer httpServer.Close()

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Title: "Test Client", Version: "1.0.0"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: httpServer.URL}, nil)
	require.NoError(t, err)
	defer session.Close()
	_, err = session.CallTool(ctx, &mcp.CallToolParams{Name: "echo", Arguments: map[string]any{}})
	require.NoError(t, err)

	tracer.Flush()
	want := []string{"mcp_client_name:test-client", "mcp_client_title:Test Client", "mcp_client_version:1.0.0"}
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
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":`+tc.params+`}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Accept", "application/json, text/event-stream")
			req.Header.Set("Mcp-Protocol-Version", "2025-06-18")
			w := httptest.NewRecorder()
			newClientInfoTestHandler(true).ServeHTTP(w, req)
			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			require.NotContains(t, w.Body.String(), `"error"`)

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
