package validationtest

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	memcachetest "gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/validationtest/integrations/gomemcache/memcache"
	dnstest "gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/validationtest/integrations/miekg/dns"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Integration is an interface that should be implemented by integrations (packages under the contrib/ folder) in
// order to be tested.
type Integration interface {
	// Name returns name of the integration (usually the import path starting from /contrib).
	Name() string

	// Init initializes the integration (start a server in the background, initialize the client, etc.).
	// It should return a cleanup function that will be executed after the test finishes.
	// It should also call t.Helper() before making any assertions.
	Init(t *testing.T) func()

	// GenSpans performs any operation(s) from the integration that generate spans.
	// It should call t.Helper() before making any assertions.
	GenSpans(t *testing.T)

	// NumSpans returns the number of spans that should have been generated during the test.
	NumSpans() int
}

func TestIntegrations(t *testing.T) {
	integrations := []Integration{
		memcachetest.New(),
		dnstest.New(),
	}
	for _, ig := range integrations {
		name := ig.Name()
		t.Run(name, func(t *testing.T) {
			sessionToken := fmt.Sprintf("%s-%d", name, time.Now().Unix())
			t.Setenv("DD_SERVICE", "Datadog-Test-Agent-Trace-Checks")
			t.Setenv("CI_TEST_AGENT_SESSION_TOKEN", sessionToken)
			t.Setenv("DD_TRACE_SPAN_ATTRIBUTE_SCHEMA", "v1")

			tracer.Start(tracer.WithAgentAddr("localhost:9126"))
			defer tracer.Stop()

			cleanup := ig.Init(t)
			defer cleanup()

			ig.GenSpans(t)

			tracer.Flush()

			assertNumSpans(t, sessionToken, ig.NumSpans())
			checkFailures(t, sessionToken)
		})
	}
}

func assertNumSpans(t *testing.T, sessionToken string, wantSpans int) {
	t.Helper()
	run := func() bool {
		req, err := http.NewRequest("GET", "http://localhost:9126/test/session/traces", nil)
		require.NoError(t, err)
		req.Header.Set("X-Datadog-Test-Session-Token", sessionToken)

		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)

		defer resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode)

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)

		var traces [][]map[string]interface{}
		require.NoError(t, json.Unmarshal(body, &traces))

		receivedSpans := 0
		for _, traceSpans := range traces {
			receivedSpans += len(traceSpans)
		}
		if receivedSpans > wantSpans {
			t.Fatalf("received more spans than expected (wantSpans: %d, receivedSpans: %d)", wantSpans, receivedSpans)
		}
		return receivedSpans == wantSpans
	}

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	timeoutChan := time.After(5 * time.Second)

	for {
		if done := run(); done {
			return
		}
		select {
		case <-ticker.C:
			continue

		case <-timeoutChan:
			t.Fatal("timeout waiting for spans")
		}
	}
}

func checkFailures(t *testing.T, sessionToken string) {
	t.Helper()
	req, err := http.NewRequest("GET", "http://localhost:9126/test/trace_check/failures", nil)
	require.NoError(t, err)
	req.Header.Set("X-Datadog-Test-Session-Token", sessionToken)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)

	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		return
	}
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	assert.Fail(t, "test agent detected failures: \n", string(body))
}
