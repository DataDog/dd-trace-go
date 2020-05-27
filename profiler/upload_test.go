// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2020 Datadog, Inc.

package profiler

import (
	"fmt"
	"io"
	"io/ioutil"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/version"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTryUpload(t *testing.T) {
	// Force an empty containerid on this test.
	defer func(cid string) { containerID = cid }(containerID)
	containerID = ""

	srv, srvURL, waiter := makeTestServer(t, 200)
	defer srv.Close()
	p := unstartedProfiler(
		WithAgentAddr(srvURL.Host),
		WithService("my-service"),
		WithEnv("my-env"),
		WithTags("tag1:1", "tag2:2"),
	)
	bat := makeFakeBatch()
	err := p.doRequest(bat)
	require.NoError(t, err)
	header, fields, tags := waiter()

	assert := assert.New(t)
	assert.Empty(header.Get("Datadog-Container-ID"))
	assert.ElementsMatch([]string{
		"host:my-host",
		"runtime:go",
		"service:my-service",
		"env:my-env",
		"tag1:1",
		"tag2:2",
		fmt.Sprintf("pid:%d", os.Getpid()),
		fmt.Sprintf("profiler_version:%s", version.Tag),
		fmt.Sprintf("runtime_version:%s", strings.TrimPrefix(runtime.Version(), "go")),
		fmt.Sprintf("runtime_compiler:%s", runtime.Compiler),
		fmt.Sprintf("runtime_arch:%s", runtime.GOARCH),
		fmt.Sprintf("runtime_os:%s", runtime.GOOS),
	}, tags)
	for k, v := range map[string]string{
		"format":   "pprof",
		"runtime":  "go",
		"types[0]": "cpu",
		"data[0]":  "my-cpu-profile",
		"types[1]": "alloc_objects,alloc_space",
		"data[1]":  "my-heap-profile",
	} {
		assert.Equal(v, fields[k], k)
	}
	for _, k := range []string{"recording-start", "recording-end"} {
		_, ok := fields[k]
		assert.True(ok, k)
	}
}

func TestOldAgent(t *testing.T) {
	srv, srvURL, _ := makeTestServer(t, 404)
	defer srv.Close()
	p := unstartedProfiler(
		WithAgentAddr(srvURL.Host),
		WithService("my-service"),
		WithEnv("my-env"),
		WithTags("tag1:1", "tag2:2"),
	)
	bat := makeFakeBatch()
	err := p.doRequest(bat)
	assert.Equal(t, errOldAgent, err)
}

func TestContainerIDHeader(t *testing.T) {
	// Force a non-empty containerid on this test.
	defer func(cid string) { containerID = cid }(containerID)
	containerID = "fakeContainerID"

	srv, srvURL, waiter := makeTestServer(t, 200)
	defer srv.Close()
	p := unstartedProfiler(
		WithAgentAddr(srvURL.Host),
		WithService("my-service"),
		WithEnv("my-env"),
		WithTags("tag1:1", "tag2:2"),
	)
	bat := makeFakeBatch()
	err := p.doRequest(bat)
	require.NoError(t, err)

	header, _, _ := waiter()
	assert.Equal(t, containerID, header.Get("Datadog-Container-Id"))
}

func BenchmarkDoRequest(b *testing.B) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_, err := ioutil.ReadAll(req.Body)
		if err != nil {
			b.Fatal(err)
		}
		req.Body.Close()
		w.WriteHeader(200)
	}))
	defer srv.Close()
	prof := profile{
		types: []string{"alloc_objects"},
		data:  []byte("my-heap-profile"),
	}
	bat := batch{
		start:    time.Now().Add(-10 * time.Second),
		end:      time.Now(),
		host:     "my-host",
		profiles: []*profile{&prof},
	}
	p := unstartedProfiler()
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		p.doRequest(bat)
	}
}

type testServerWaiter func() (http.Header, map[string]string, []string)

func makeTestServer(t *testing.T, statusCode int) (*httptest.Server, *url.URL, testServerWaiter) {
	wait := make(chan struct{})
	var header http.Header
	fields := make(map[string]string)
	var tags []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(statusCode)
		header = req.Header
		_, params, err := mime.ParseMediaType(req.Header.Get("Content-Type"))
		if err != nil {
			t.Fatal(err)
		}
		mr := multipart.NewReader(req.Body, params["boundary"])
		defer req.Body.Close()
		for {
			p, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			slurp, err := ioutil.ReadAll(p)
			if err != nil {
				t.Fatal(err)
			}
			switch k := p.FormName(); k {
			case "tags[]":
				tags = append(tags, string(slurp))
			default:
				fields[k] = string(slurp)
			}
		}
		close(wait)
	}))

	srvURL, err := url.Parse(srv.URL)
	if err != nil {
		srv.Close()
		t.Fatalf("failed to parse server url")
		return nil, nil, nil
	}

	waiter := func() (http.Header, map[string]string, []string) {
		select {
		case <-wait:
			// OK
			return header, fields, tags
		case <-time.After(time.Second):
			t.Fatalf("timeout")
			return nil, nil, nil
		}
	}

	return srv, srvURL, waiter
}

func makeFakeBatch() batch {
	cpu := profile{
		types: []string{"cpu"},
		data:  []byte("my-cpu-profile"),
	}
	heap := profile{
		types: []string{"alloc_objects", "alloc_space"},
		data:  []byte("my-heap-profile"),
	}
	return batch{
		start:    time.Now().Add(-10 * time.Second),
		end:      time.Now(),
		host:     "my-host",
		profiles: []*profile{&cpu, &heap},
	}
}
