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
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTryUpload(t *testing.T) {
	wait := make(chan struct{})
	fields := make(map[string]string)
	var tags []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(200)
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

	defer srv.Close()
	defer func(old *http.Client) { httpClient = old }(httpClient)
	p := unstartedProfiler(
		WithURL(srv.URL+"/"),
		WithService("my-service"),
		WithEnv("my-env"),
		WithTags("tag1:1", "tag2:2"),
	)
	cpu := profile{
		types: []string{"cpu"},
		data:  []byte("my-cpu-profile"),
	}
	heap := profile{
		types: []string{"alloc_objects", "alloc_space"},
		data:  []byte("my-heap-profile"),
	}
	bat := batch{
		start:    time.Now().Add(-10 * time.Second),
		end:      time.Now(),
		host:     "my-host",
		profiles: []*profile{&cpu, &heap},
	}
	err := p.doRequest(bat)
	require.NoError(t, err)
	select {
	case <-wait:
		// OK
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}

	assert := assert.New(t)
	assert.ElementsMatch([]string{
		"host:my-host",
		"runtime:go",
		"service:my-service",
		"env:my-env",
		"tag1:1",
		"tag2:2",
		fmt.Sprintf("pid:%d", os.Getpid()),
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
