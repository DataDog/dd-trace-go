// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package profiler

import (
	"fmt"
	"io"
	"io/ioutil"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
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

	srv := startHTTPTestServer(t, 200)
	defer srv.close()
	p, err := unstartedProfiler(
		WithAgentAddr(srv.address),
		WithService("my-service"),
		WithEnv("my-env"),
		WithTags("tag1:1", "tag2:2"),
	)
	require.NoError(t, err)
	err = p.doRequest(testBatch)
	require.NoError(t, err)
	header, fields, tags := srv.wait()

	assert := assert.New(t)
	assert.Empty(header.Get("Datadog-Container-ID"))
	assert.ElementsMatch([]string{
		"host:my-host",
		"runtime:go",
		"service:my-service",
		"env:my-env",
		"tag1:1",
		"tag2:2",
		fmt.Sprintf("process_id:%d", os.Getpid()),
		fmt.Sprintf("profiler_version:%s", version.Tag),
		fmt.Sprintf("runtime_version:%s", strings.TrimPrefix(runtime.Version(), "go")),
		fmt.Sprintf("runtime_compiler:%s", runtime.Compiler),
		fmt.Sprintf("runtime_arch:%s", runtime.GOARCH),
		fmt.Sprintf("runtime_os:%s", runtime.GOOS),
		fmt.Sprintf("runtime-id:%s", globalconfig.RuntimeID()),
	}, tags)
	for k, v := range map[string]string{
		"version":          "3",
		"family":           "go",
		"data[cpu.pprof]":  "my-cpu-profile",
		"data[heap.pprof]": "my-heap-profile",
	} {
		assert.Equal(v, fields[k], k)
	}
	for _, k := range []string{"start", "end"} {
		_, ok := fields[k]
		assert.True(ok, "key should be present: %s", k)
	}
	for _, k := range []string{"runtime", "format"} {
		_, ok := fields[k]
		assert.False(ok, "key should not be present: %s", k)
	}
}

func TestTryUploadUDS(t *testing.T) {
	srv := startSocketTestServer(t, 200)
	defer srv.close()
	p, err := unstartedProfiler(
		WithUDS(srv.address),
	)
	require.NoError(t, err)
	err = p.doRequest(testBatch)
	require.NoError(t, err)
	_, _, tags := srv.wait()

	assert := assert.New(t)
	assert.ElementsMatch([]string{
		"host:my-host",
		"runtime:go",
	}, tags[0:2])
}

func Test202Accepted(t *testing.T) {
	srv := startHTTPTestServer(t, 202)
	defer srv.close()
	p, err := unstartedProfiler(
		WithAgentAddr(srv.address),
		WithService("my-service"),
		WithEnv("my-env"),
		WithTags("tag1:1", "tag2:2"),
	)
	require.NoError(t, err)
	err = p.doRequest(testBatch)
	require.NoError(t, err)
}

func TestOldAgent(t *testing.T) {
	srv := startHTTPTestServer(t, 404)
	defer srv.close()
	p, err := unstartedProfiler(
		WithAgentAddr(srv.address),
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

	srv := startHTTPTestServer(t, 200)
	defer srv.close()
	p, err := unstartedProfiler(
		WithAgentAddr(srv.address),
		WithService("my-service"),
		WithEnv("my-env"),
		WithTags("tag1:1", "tag2:2"),
	)
	require.NoError(t, err)
	err = p.doRequest(testBatch)
	require.NoError(t, err)

	header, _, _ := srv.wait()
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

type testServerWaiter func() (http.Header, map[string]string, []string)

type testServer struct {
	t       *testing.T
	handler http.HandlerFunc

	// for recording request state
	waitc  chan struct{}
	header http.Header
	fields map[string]string
	tags   []string

	address string
	close   func() error
}

func newTestServer(t *testing.T, statusCode int) *testServer {
	ts := &testServer{
		t:      t,
		waitc:  make(chan struct{}),
		fields: make(map[string]string),
	}
	ts.handler = http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		t.Helper()
		w.WriteHeader(statusCode)
		ts.header = req.Header
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
				ts.tags = append(ts.tags, string(slurp))
			default:
				ts.fields[k] = string(slurp)
			}
		}
		close(ts.waitc)
	})
	return ts
}

func (ts *testServer) wait() (http.Header, map[string]string, []string) {
	ts.t.Helper()
	select {
	case <-ts.waitc:
		// OK
		return ts.header, ts.fields, ts.tags
	case <-time.After(time.Second):
		ts.t.Fatalf("timeout")
		return nil, nil, nil
	}
}

func startHTTPTestServer(t *testing.T, statusCode int) *testServer {
	t.Helper()
	ts := newTestServer(t, statusCode)
	server := httptest.NewServer(ts.handler)
	ts.close = func() error {
		server.Close()
		return nil
	}

	srvURL, err := url.Parse(server.URL)
	if err != nil {
		server.Close()
		t.Fatalf("failed to parse server url")
		return nil
	}
	ts.address = srvURL.Host

	return ts
}

func startSocketTestServer(t *testing.T, statusCode int) *testServer {
	t.Helper()
	udsPath := "/tmp/com.datadoghq.dd-trace-go.profiler.test.sock"
	ts := newTestServer(t, statusCode)
	server := http.Server{Handler: ts.handler}
	ts.close = server.Close
	ts.address = udsPath

	unixListener, err := net.Listen("unix", udsPath)
	if err != nil {
		t.Fatal(err)
	}
	go server.Serve(unixListener)

	return ts
}
