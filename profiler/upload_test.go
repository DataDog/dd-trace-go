// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package profiler

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/version"

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

func TestTryUpload(t *testing.T) {
	// Force an empty containerid on this test.
	defer func(cid string) { containerID = cid }(containerID)
	containerID = ""

	profiles := make(chan profileMeta, 1)
	server := httptest.NewServer(&mockBackend{t: t, profiles: profiles})
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
	profile := <-profiles

	assert := assert.New(t)
	assert.Empty(profile.headers.Get("Datadog-Container-ID"))
	assert.ElementsMatch([]string{
		"host:my-host",
		"runtime:go",
		"service:my-service",
		"env:my-env",
		"profile_seq:23",
		"tag1:1",
		"tag2:2",
		fmt.Sprintf("process_id:%d", os.Getpid()),
		fmt.Sprintf("profiler_version:%s", version.Tag),
		fmt.Sprintf("runtime_version:%s", strings.TrimPrefix(runtime.Version(), "go")),
		fmt.Sprintf("runtime_compiler:%s", runtime.Compiler),
		fmt.Sprintf("runtime_arch:%s", runtime.GOARCH),
		fmt.Sprintf("runtime_os:%s", runtime.GOOS),
		fmt.Sprintf("runtime-id:%s", globalconfig.RuntimeID()),
	}, profile.tags)
	assert.Equal(profile.event.Version, "4")
	assert.Equal(profile.event.Family, "go")
	assert.NotNil(profile.event.Start)
	assert.NotNil(profile.event.End)
	for k, v := range map[string][]byte{
		"cpu.pprof":  []byte("my-cpu-profile"),
		"heap.pprof": []byte("my-heap-profile"),
	} {
		assert.Equal(v, profile.attachments[k])
	}
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

func TestContainerIDHeader(t *testing.T) {
	// Force a non-empty containerid on this test.
	defer func(cid string) { containerID = cid }(containerID)
	containerID = "fakeContainerID"

	profiles := make(chan profileMeta, 1)
	server := httptest.NewServer(&mockBackend{t: t, profiles: profiles})
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

	profile := <-profiles
	assert.Equal(t, containerID, profile.headers.Get("Datadog-Container-Id"))
}

func BenchmarkDoRequest(b *testing.B) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_, err := io.ReadAll(req.Body)
		if err != nil {
			b.Fatal(err)
		}
		req.Body.Close()
		w.WriteHeader(200)
	}))
	defer srv.Close()
	prof := profile{
		name: "heap",
		data: []byte("my-heap-profile"),
	}
	bat := batch{
		start:    time.Now().Add(-10 * time.Second),
		end:      time.Now(),
		host:     "my-host",
		profiles: []*profile{&prof},
	}
	p, err := unstartedProfiler()
	require.NoError(b, err)
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		p.doRequest(bat)
	}
}
