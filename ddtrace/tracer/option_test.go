// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"encoding/json"
	"io/ioutil"
	"net"
	"os"
	"os/exec"
	"strings"
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testStatsd asserts that the given statsd.Client can successfully send metrics
// to a UDP listener located at addr.
func testStatsd(t *testing.T, cfg *config, addr string) {
	client, err := newStatsdClient(cfg)
	require.NoError(t, err)
	defer client.Close()
	require.Equal(t, addr, cfg.dogstatsdAddr)
	_, err = net.ResolveUDPAddr("udp", addr)
	require.NoError(t, err)

	client.Count("name", 1, []string{"tag"}, 1)
	require.NoError(t, client.Close())
}

// clearIntegreationsForTests clears the state of all integrations
func clearIntegrationsForTests() {
	for name, state := range contribIntegrations {
		state.imported = false
		contribIntegrations[name] = state
	}
}

type contribPkg struct {
	Dir        string
	Root       string
	ImportPath string
	Name       string
}

func TestIntegrationEnabled(t *testing.T) {
	body, err := exec.Command("go", "list", "-json", "../../contrib/...").Output()
	if err != nil {
		t.Fatalf(err.Error())
	}
	var packages []contribPkg
	stream := json.NewDecoder(strings.NewReader(string(body)))
	for stream.More() {
		var out contribPkg
		err := stream.Decode(&out)
		if err != nil {
			t.Fatalf(err.Error())
		}
		packages = append(packages, out)
	}
	for _, pkg := range packages {
		if strings.Contains(pkg.ImportPath, "/test") || strings.Contains(pkg.ImportPath, "/internal") {
			continue
		}
		p := strings.Replace(pkg.Dir, pkg.Root, "../..", 1)
		body, err := exec.Command("grep", "-rl", "MarkIntegrationImported", p).Output()
		if err != nil {
			t.Fatalf(err.Error())
		}
		assert.NotEqual(t, len(body), 0, "expected %s to call MarkIntegrationImported", pkg.Name)
	}
}

func TestDefaultHTTPClient(t *testing.T) {
	t.Run("no-socket", func(t *testing.T) {
		// We care that whether clients are different, but doing a deep
		// comparison is overkill and can trigger the race detector, so
		// just compare the pointers.
		assert.Same(t, defaultHTTPClient(), defaultClient)
	})

	t.Run("socket", func(t *testing.T) {
		f, err := ioutil.TempFile("", "apm.socket")
		if err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
		defer os.RemoveAll(f.Name())
		defer func(old string) { defaultSocketAPM = old }(defaultSocketAPM)
		defaultSocketAPM = f.Name()
		assert.NotSame(t, defaultHTTPClient(), defaultClient)
	})
}

func TestDefaultDogstatsdAddr(t *testing.T) {
	t.Run("no-socket", func(t *testing.T) {
		assert.Equal(t, defaultDogstatsdAddr(), "localhost:8125")
	})

	t.Run("env", func(t *testing.T) {
		t.Setenv("DD_DOGSTATSD_PORT", "8111")
		assert.Equal(t, defaultDogstatsdAddr(), "localhost:8111")
	})

	t.Run("env+socket", func(t *testing.T) {
		t.Setenv("DD_DOGSTATSD_PORT", "8111")
		assert.Equal(t, defaultDogstatsdAddr(), "localhost:8111")
		f, err := ioutil.TempFile("", "dsd.socket")
		if err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
		defer os.RemoveAll(f.Name())
		defer func(old string) { defaultSocketDSD = old }(defaultSocketDSD)
		defaultSocketDSD = f.Name()
		assert.Equal(t, defaultDogstatsdAddr(), "localhost:8111")
	})

	t.Run("socket", func(t *testing.T) {
		defer func(old string) { os.Setenv("DD_AGENT_HOST", old) }(os.Getenv("DD_AGENT_HOST"))
		defer func(old string) { os.Setenv("DD_DOGSTATSD_PORT", old) }(os.Getenv("DD_DOGSTATSD_PORT"))
		os.Unsetenv("DD_AGENT_HOST")
		os.Unsetenv("DD_DOGSTATSD_PORT")
		f, err := ioutil.TempFile("", "dsd.socket")
		if err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
		defer os.RemoveAll(f.Name())
		defer func(old string) { defaultSocketDSD = old }(defaultSocketDSD)
		defaultSocketDSD = f.Name()
		assert.Equal(t, defaultDogstatsdAddr(), "unix://"+f.Name())
	})
}

func TestStartWithLink(t *testing.T) {
	assert := assert.New(t)

	links := []ddtrace.SpanLink{{TraceID: 1, SpanID: 2}, {TraceID: 3, SpanID: 4}}
	span := newTracer().StartSpan("test.request", WithSpanLinks(links)).(*span)

	assert.Len(span.SpanLinks, 2)
	assert.Equal(span.SpanLinks[0].TraceID, uint64(1))
	assert.Equal(span.SpanLinks[0].SpanID, uint64(2))
	assert.Equal(span.SpanLinks[1].TraceID, uint64(3))
	assert.Equal(span.SpanLinks[1].SpanID, uint64(4))
}
