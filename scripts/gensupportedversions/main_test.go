// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var testGoVersions = []string{"1.26.11", "1.27.4"}

func fakeStdlib(path string) bool { return path == "net/http" }

// writeFiles creates files under root, keyed by their slash-separated relative path.
func writeFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	}
}

func goMod(module string, requires ...string) string {
	s := "module " + module + "\n\ngo 1.26.0\n"
	if len(requires) > 0 {
		s += "\nrequire (\n\t" + strings.Join(requires, "\n\t") + "\n)\n"
	}
	return s
}

func TestGenerate(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		// One module, auto-instrumented through Orchestrion.
		"contrib/redis/go-redis.v9/go.mod": goMod("github.com/DataDog/dd-trace-go/contrib/redis/go-redis.v9/v2",
			"github.com/redis/go-redis/v9 v9.7.3"),
		"contrib/redis/go-redis.v9/manifest.yaml":   "name: github.com/redis/go-redis/v9\npackages:\n- name: github.com/redis/go-redis/v9\n",
		"contrib/redis/go-redis.v9/orchestrion.yml": "",
		// Several modules for one integration, auto-instrumented through otelc only, with
		// an otelc hook module that needs no manifest.
		"contrib/elastic/go-elasticsearch.v6/go.mod": goMod("github.com/DataDog/dd-trace-go/contrib/elastic/go-elasticsearch.v6/v2",
			"github.com/elastic/go-elasticsearch/v6 v6.8.5", "github.com/elastic/go-elasticsearch/v7 v7.17.1"),
		"contrib/elastic/go-elasticsearch.v6/manifest.yaml": "name: github.com/elastic/go-elasticsearch/v6\npackages:\n" +
			"- name: github.com/elastic/go-elasticsearch/v6\n- name: github.com/elastic/go-elasticsearch/v7\n",
		"contrib/elastic/go-elasticsearch.v6/otelc/otelc.yaml": "",
		"contrib/elastic/go-elasticsearch.v6/otelc/go.mod":     goMod("github.com/DataDog/dd-trace-go/contrib/elastic/go-elasticsearch.v6/otelc/v2"),
		// +incompatible versions, no auto-instrumentation.
		"contrib/twitchtv/twirp/go.mod":        goMod("github.com/DataDog/dd-trace-go/contrib/twitchtv/twirp/v2", "github.com/twitchtv/twirp v8.1.3+incompatible"),
		"contrib/twitchtv/twirp/manifest.yaml": "name: github.com/twitchtv/twirp\npackages:\n- name: github.com/twitchtv/twirp\n",
		// Standard library integration.
		"contrib/net/http/go.mod":        goMod("github.com/DataDog/dd-trace-go/contrib/net/http/v2"),
		"contrib/net/http/manifest.yaml": "name: net/http\npackages: []\n",
		// Service without a traced library: skipped.
		"contrib/haproxy/stream-processing-offload/go.mod":        goMod("github.com/DataDog/dd-trace-go/contrib/haproxy/stream-processing-offload/v2"),
		"contrib/haproxy/stream-processing-offload/manifest.yaml": "name: haproxy/stream-processing-offload\npackages: []\n",
		// Test fixture outside the published module path: needs no manifest.
		"contrib/aws/datadog-lambda-go/test/hello/go.mod": goMod("github.com/DataDog/dd-trace-go/v2/contrib/aws/datadog-lambda-go/test/hello"),
	})

	entries, err := generate(root, testGoVersions, fakeStdlib)
	require.NoError(t, err)

	runtimes := map[string][]string{"go": testGoVersions}
	group := func(supported string, tested ...string) []versionGroup {
		if tested == nil {
			tested = []string{}
		}
		return []versionGroup{{TestedRuntimes: runtimes, SupportedRange: supported, Tested: tested}}
	}
	assert.Equal(t, []entry{
		{
			LibraryName:      "github.com/elastic/go-elasticsearch/v6",
			IntegrationName:  "github.com/elastic/go-elasticsearch/v6",
			AutoInstrumented: true,
			Versions:         group(">=v6.8.5", "v6.8.5"),
		},
		{
			LibraryName:      "github.com/elastic/go-elasticsearch/v7",
			IntegrationName:  "github.com/elastic/go-elasticsearch/v6",
			AutoInstrumented: true,
			Versions:         group(">=v7.17.1", "v7.17.1"),
		},
		{
			LibraryName:      "github.com/redis/go-redis/v9",
			IntegrationName:  "github.com/redis/go-redis/v9",
			AutoInstrumented: true,
			Versions:         group(">=v9.7.3", "v9.7.3"),
		},
		{
			LibraryName:     "github.com/twitchtv/twirp",
			IntegrationName: "github.com/twitchtv/twirp",
			Versions:        group(">=v8.1.3", "v8.1.3+incompatible"),
		},
		{
			LibraryName:      "net/http",
			IntegrationName:  "net/http",
			AutoInstrumented: false,
			Versions:         group("*"),
		},
	}, entries)
}

func TestGenerateErrors(t *testing.T) {
	for name, files := range map[string]map[string]string{
		"contrib module without a manifest": {
			"contrib/gin-gonic/gin/go.mod": goMod("github.com/DataDog/dd-trace-go/contrib/gin-gonic/gin/v2"),
		},
		"manifest package missing from go.mod": {
			"contrib/gin-gonic/gin/go.mod":        goMod("github.com/DataDog/dd-trace-go/contrib/gin-gonic/gin/v2"),
			"contrib/gin-gonic/gin/manifest.yaml": "name: github.com/gin-gonic/gin\npackages:\n- name: github.com/gin-gonic/gin\n",
		},
		"manifest without a name": {
			"contrib/gin-gonic/gin/go.mod":        goMod("github.com/DataDog/dd-trace-go/contrib/gin-gonic/gin/v2"),
			"contrib/gin-gonic/gin/manifest.yaml": "packages: []\n",
		},
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			writeFiles(t, root, files)
			_, err := generate(root, testGoVersions, fakeStdlib)
			assert.Error(t, err)
		})
	}
}

// TestGenerateRepo runs the generator on the repository, so a manifest or go.mod change
// that breaks it fails here and not only in the GitLab pipeline.
func TestGenerateRepo(t *testing.T) {
	entries, err := generate(filepath.Join("..", ".."), testGoVersions, isStdlib)
	require.NoError(t, err)
	assert.NotEmpty(t, entries)
	seen := map[[2]string]bool{}
	for _, e := range entries {
		key := [2]string{e.LibraryName, e.IntegrationName}
		assert.False(t, seen[key], "duplicate entry %v", key)
		seen[key] = true
	}
}

func TestIsStdlib(t *testing.T) {
	assert.True(t, isStdlib("net/http"))
	assert.True(t, isStdlib("log/slog"))
	assert.False(t, isStdlib("haproxy/stream-processing-offload"))
	assert.False(t, isStdlib("github.com/redis/go-redis/v9"))
}

func TestFetchGoVersions(t *testing.T) {
	defer func(d time.Duration) { retryDelay = d }(retryDelay)
	retryDelay = time.Millisecond
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte(`[
			{"version": "go1.28rc1", "stable": false},
			{"version": "go1.27.4", "stable": true},
			{"version": "go1.27.3", "stable": true},
			{"version": "go1.26.11", "stable": true},
			{"version": "go1.25.9", "stable": true}
		]`))
	}))
	defer srv.Close()

	versions, err := fetchGoVersions(context.Background(), srv.Client(), srv.URL)
	require.NoError(t, err)
	assert.Equal(t, []string{"1.26.11", "1.27.4"}, versions)
	assert.EqualValues(t, 2, calls.Load())
}

func TestLatestStableNeedsTwoMinors(t *testing.T) {
	_, err := latestStable([]goRelease{{Version: "go1.27.4", Stable: true}, {Version: "go1.27.3", Stable: true}})
	assert.Error(t, err)
}
