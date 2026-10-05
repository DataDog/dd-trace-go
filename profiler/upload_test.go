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
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/v2/instrumentation/httpmem"
	maininternal "github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/processtags"
	"github.com/DataDog/dd-trace-go/v2/internal/version"

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
	backend := &fakeBackend{profiles: profiles}
	server := httptest.NewUnstartedServer(backend)
	udsPath := "/tmp/com.datadoghq.dd-trace-go.profiler.test.sock"
	l, err := net.Listen("unix", udsPath)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	server.Listener = l
	server.Start()
	defer server.Close()

	require.NoError(t, Start(
		WithUDS(udsPath),
		WithProfileTypes(),
		WithPeriod(10*time.Millisecond),
	))
	defer Stop()

	profile := backend.ReceiveProfile(t)
	assert.Contains(t, profile.tags, "runtime:go")
}

func Test202Accepted(t *testing.T) {
	// startTestProfiler's fakeBackend returns 202; ReceiveProfile confirms
	// the profiler treats 202 as success and continues uploading.
	backend := startTestProfiler(t, 1,
		WithProfileTypes(),
		WithPeriod(10*time.Millisecond),
		WithService("my-service"),
		WithEnv("my-env"),
		WithTags("tag1:1", "tag2:2"),
	)
	backend.ReceiveProfile(t)
}

func TestOldAgent(t *testing.T) {
	ch := make(chan struct{}, 2)
	server, client := httpmem.ServerAndClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/profiling/v1/input" {
			return
		}
		w.WriteHeader(http.StatusNotFound)
		select {
		case ch <- struct{}{}:
		default:
		}
	}))
	defer server.Close()
	rl := &log.RecordLogger{}
	defer log.UseLogger(rl)()

	Start(WithHTTPClient(client), WithProfileTypes(), WithPeriod(time.Millisecond))
	defer Stop()
	<-ch
	<-ch
	log.Flush()
	logs := rl.Logs()
	const want = "Datadog Agent is not accepting profiles"
	if !slices.ContainsFunc(logs, func(s string) bool {
		return strings.Contains(s, "Datadog Agent is not accepting profiles")
	}) {
		t.Errorf("didn't see log message containing %s, got: %s", want, logs)
	}
}

func setContainerEntityIDs(t *testing.T, cid, eid string) {
	t.Helper()
	origCID := containerID.Load()
	origEID := entityID.Load()
	t.Cleanup(func() {
		containerID.Store(origCID)
		entityID.Store(origEID)
	})
	containerID.Store(&cid)
	entityID.Store(&eid)
}

func TestEntityContainerIDHeaders(t *testing.T) {
	t.Run("set", func(t *testing.T) {
		setContainerEntityIDs(t, "fakeContainerID", "fakeEntityID")
		profile := doOneShortProfileUpload(t)
		assert.Equal(t, "fakeContainerID", profile.headers.Get("Datadog-Container-Id"))
		assert.Equal(t, "fakeEntityID", profile.headers.Get("Datadog-Entity-Id"))
	})
	t.Run("unset", func(t *testing.T) {
		setContainerEntityIDs(t, "", "")
		profile := doOneShortProfileUpload(t)
		assert.Empty(t, profile.headers.Get("Datadog-Container-ID"))
		assert.Empty(t, profile.headers.Get("Datadog-Entity-ID"))
	})
}

func TestEVPOriginHeader(t *testing.T) {
	profile := doOneShortProfileUpload(t)
	assert.Equal(t, "dd-trace-go", profile.headers.Get("DD-EVP-Origin"))
	assert.Equal(t, version.Tag, profile.headers.Get("DD-EVP-Origin-Version"))
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

func TestProcessTags(t *testing.T) {
	t.Run("enabled", func(t *testing.T) {
		profile := doOneShortProfileUpload(t)
		assert.NotEmpty(t, profile.event.ProcessTags)
	})
	t.Run("disabled", func(t *testing.T) {
		t.Setenv("DD_EXPERIMENTAL_PROPAGATE_PROCESS_TAGS_ENABLED", "false")
		processtags.Reload()

		profile := doOneShortProfileUpload(t)
		assert.Empty(t, profile.event.ProcessTags)
	})
}
