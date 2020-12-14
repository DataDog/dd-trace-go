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
			types: []string{"cpu"},
			data:  []byte("my-cpu-profile"),
		},
		{
			types: []string{"alloc_objects", "alloc_space"},
			data:  []byte("my-heap-profile"),
		},
	},
}

func TestTryUpload(t *testing.T) {
	// Force an empty containerid on this test.
	defer func(cid string) { containerID = cid }(containerID)
	containerID = ""

	fixtures := []struct {
		description  string
		startService startServer
		exopts       func(string) []Option
	}{
		{
			description:  "test against httptest.Server",
			startService: makeTestServer,
			exopts: func(host string) []Option {
				return []Option{WithAgentAddr(host)}
			},
		},
		{
			description:  "test against Unix Domain Socket server",
			startService: makeSocketServer,
			exopts: func(udsPath string) []Option {
				return []Option{WithUDS(udsPath)}
			},
		},
	}

	for _, f := range fixtures {
		t.Run(f.description, func(t *testing.T) {
			srv, address, waiter := f.startService(t, 200)
			defer srv.Close()

			opts := []Option{
				WithService("my-service"),
				WithEnv("my-env"),
				WithTags("tag1:1", "tag2:2"),
			}
			p, err := unstartedProfiler(append(opts, f.exopts(address)...)...)
			require.NoError(t, err)
			err = p.doRequest(testBatch)
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
				fmt.Sprintf("runtime-id:%s", globalconfig.RuntimeID()),
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
		})
	}
}

func TestOldAgent(t *testing.T) {
	srv, host, _ := makeTestServer(t, 404)
	defer srv.Close()
	p, err := unstartedProfiler(
		WithAgentAddr(host),
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

	srv, host, waiter := makeTestServer(t, 200)
	defer srv.Close()
	p, err := unstartedProfiler(
		WithAgentAddr(host),
		WithService("my-service"),
		WithEnv("my-env"),
		WithTags("tag1:1", "tag2:2"),
	)
	require.NoError(t, err)
	err = p.doRequest(testBatch)
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
	p, err := unstartedProfiler()
	require.NoError(b, err)
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		p.doRequest(bat)
	}
}

type testServerWaiter func() (http.Header, map[string]string, []string)

type makeServer func(handler http.Handler) (io.Closer, string)
type startServer func(*testing.T, int) (io.Closer, string, testServerWaiter)

func startTestServer(t *testing.T, statusCode int, newServer makeServer) (io.Closer, string, testServerWaiter) {
	wait := make(chan struct{})
	var header http.Header
	fields := make(map[string]string)
	var tags []string

	srv, path := newServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
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

	return srv, path, waiter
}

type testServer struct {
	s *httptest.Server
}

func (ts testServer) Close() error {
	ts.s.Close()
	return nil
}

func makeTestServer(t *testing.T, statusCode int) (io.Closer, string, testServerWaiter) {
	return startTestServer(t, statusCode, func(handler http.Handler) (io.Closer, string) {
		srv := httptest.NewServer(handler)
		srvURL, err := url.Parse(srv.URL)
		if err != nil {
			srv.Close()
			t.Fatalf("failed to parse server url")
			return nil, ""
		}
		return testServer{srv}, srvURL.Host
	})
}

func makeSocketServer(t *testing.T, statusCode int) (io.Closer, string, testServerWaiter) {
	return startTestServer(t, statusCode, func(handler http.Handler) (io.Closer, string) {
		srv := &http.Server{Handler: handler}
		udsPath := "/tmp/com.datadoghq.dd-trace-go.profiler.test.sock"
		unixListener, err := net.Listen("unix", udsPath)
		if err != nil {
			t.Fatal(err)
		}
		go srv.Serve(unixListener)
		return srv, udsPath
	})
}
