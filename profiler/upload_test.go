// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package profiler

import (
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"
	"time"

	maininternal "gopkg.in/DataDog/dd-trace-go.v1/internal"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var testBatch = batch{
	seq:   23,
	start: time.Now().Add(-10 * time.Second),
	end:   time.Now(),
	host:  "my-host",
	profiles: []*profile{
		{
			name: CPUProfile.Filename(),
			data: []byte("my-cpu-profile"),
		},
		{
			name: HeapProfile.Filename(),
			data: []byte("my-heap-profile"),
		},
	},
}

func TestTryUploadUDS(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix domain sockets are non-functional on windows.")
	}
	profiles := make(chan profileMeta, 1)
	server := httptest.NewUnstartedServer(&mockBackend{t: t, profiles: profiles})
	udsPath := "/tmp/com.datadoghq.dd-trace-go.profiler.test.sock"
	l, err := net.Listen("unix", udsPath)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	server.Listener = l
	server.Start()
	defer server.Close()

	p, err := unstartedProfiler(
		WithUDS(udsPath),
	)
	require.NoError(t, err)
	err = p.doRequest(testBatch)
	require.NoError(t, err)
	profile := <-profiles

	assert := assert.New(t)
	assert.Subset(profile.tags, []string{
		"host:my-host",
		"runtime:go",
	})
}

func Test202Accepted(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()
	p, err := unstartedProfiler(
		WithAgentAddr(server.Listener.Addr().String()),
		WithService("my-service"),
		WithEnv("my-env"),
		WithTags("tag1:1", "tag2:2"),
	)
	require.NoError(t, err)
	err = p.doRequest(testBatch)
	require.NoError(t, err)
}

func TestOldAgent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	p, err := unstartedProfiler(
		WithAgentAddr(server.Listener.Addr().String()),
		WithService("my-service"),
		WithEnv("my-env"),
		WithTags("tag1:1", "tag2:2"),
	)
	require.NoError(t, err)
	err = p.doRequest(testBatch)
	assert.Equal(t, errOldAgent, err)
}

func TestEntityContainerIDHeaders(t *testing.T) {
	t.Run("set", func(t *testing.T) {
		defer func(cid, eid string) { containerID = cid; entityID = eid }(containerID, entityID)
		entityID = "fakeEntityID"
		containerID = "fakeContainerID"
		profile := doOneShortProfileUpload(t)
		assert.Equal(t, containerID, profile.headers.Get("Datadog-Container-Id"))
		assert.Equal(t, entityID, profile.headers.Get("Datadog-Entity-Id"))
	})
	t.Run("unset", func(t *testing.T) {
		// Force an empty containerid and entityID on this test.
		defer func(cid, eid string) { containerID = cid; entityID = eid }(containerID, entityID)
		entityID = ""
		containerID = ""
		profile := doOneShortProfileUpload(t)
		assert.Empty(t, profile.headers.Get("Datadog-Container-ID"))
		assert.Empty(t, profile.headers.Get("Datadog-Entity-ID"))
	})
}

func TestGitMetadata(t *testing.T) {
	t.Run("git-metadata-from-dd-tags", func(t *testing.T) {
		t.Setenv(maininternal.EnvDDTags, "git.commit.sha:123456789ABCD git.repository_url:github.com/user/repo go_path:somepath")
		maininternal.RefreshGitMetadataTags()

		profile := doOneShortProfileUpload(t)

		assert := assert.New(t)
		assert.Contains(profile.tags, "git.commit.sha:123456789ABCD")
		assert.Contains(profile.tags, "git.repository_url:github.com/user/repo")
		assert.Contains(profile.tags, "go_path:somepath")
	})
	t.Run("git-metadata-from-dd-tags-with-credentials", func(t *testing.T) {
		t.Setenv(maininternal.EnvDDTags, "git.commit.sha:123456789ABCD git.repository_url:http://u@github.com/user/repo go_path:somepath")
		maininternal.RefreshGitMetadataTags()

		profile := doOneShortProfileUpload(t)

		assert := assert.New(t)
		assert.Contains(profile.tags, "git.commit.sha:123456789ABCD")
		assert.Contains(profile.tags, "git.repository_url:http://github.com/user/repo")
		assert.Contains(profile.tags, "go_path:somepath")
	})
	t.Run("git-metadata-from-env", func(t *testing.T) {
		t.Setenv(maininternal.EnvDDTags, "git.commit.sha:123456789ABCD git.repository_url:github.com/user/repo")

		// git metadata env has priority under DD_TAGS
		t.Setenv(maininternal.EnvGitRepositoryURL, "github.com/user/repo_new")
		t.Setenv(maininternal.EnvGitCommitSha, "123456789ABCDE")
		maininternal.RefreshGitMetadataTags()

		profile := doOneShortProfileUpload(t)

		assert := assert.New(t)
		assert.Contains(profile.tags, "git.commit.sha:123456789ABCDE")
		assert.Contains(profile.tags, "git.repository_url:github.com/user/repo_new")
	})
	t.Run("git-metadata-from-env-with-credentials", func(t *testing.T) {
		t.Setenv(maininternal.EnvGitRepositoryURL, "https://u@github.com/user/repo_new")
		t.Setenv(maininternal.EnvGitCommitSha, "123456789ABCDE")
		maininternal.RefreshGitMetadataTags()

		profile := doOneShortProfileUpload(t)

		assert := assert.New(t)
		assert.Contains(profile.tags, "git.commit.sha:123456789ABCDE")
		assert.Contains(profile.tags, "git.repository_url:https://github.com/user/repo_new")
	})

	t.Run("git-metadata-disabled", func(t *testing.T) {
		t.Setenv(maininternal.EnvGitMetadataEnabledFlag, "false")
		t.Setenv(maininternal.EnvDDTags, "git.commit.sha:123456789ABCD git.repository_url:github.com/user/repo")
		t.Setenv(maininternal.EnvGitRepositoryURL, "github.com/user/repo")
		t.Setenv(maininternal.EnvGitCommitSha, "123456789ABCD")
		maininternal.RefreshGitMetadataTags()

		profile := doOneShortProfileUpload(t)

		assert := assert.New(t)
		assert.NotContains(profile.tags, "git.commit.sha:123456789ABCD")
		assert.NotContains(profile.tags, "git.repository_url:github.com/user/repo")
	})
}
