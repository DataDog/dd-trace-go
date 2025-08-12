package nethttp

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestCaseGlobalFunctions tests the aspect replacing global functions.
type TestCaseGlobalFunctions struct {
	base
}

func (b *TestCaseGlobalFunctions) Run(_ context.Context, t *testing.T) {
	cl := newHttpClient(b.srv.Addr)

	resp, err := cl.Get("/")
	require.NoError(t, err)
	require.Equal(t, http.StatusTeapot, resp.StatusCode)
}

type httpClient struct {
	serverHost string
}

func newHttpClient(serverHost string) *httpClient {
	return &httpClient{
		serverHost: serverHost,
	}
}

func (client *httpClient) Get(path string) (*http.Response, error) {
	serverHost := client.serverHost

	return http.Get("http://" + serverHost + path)
}
