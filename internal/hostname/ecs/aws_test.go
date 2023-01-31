package ecs

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetLaunchType(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/task", r.URL.Path)
		w.Write([]byte(`{"LaunchType":"FARGATE"}`))
	}))
	defer ts.Close()
	defer func(old string) { metadataURL = old }(metadataURL)
	metadataURL = ts.URL

	result, err := GetLaunchType(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "FARGATE", result)
}
