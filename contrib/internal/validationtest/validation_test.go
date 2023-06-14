package validationtest

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	memcachetest "gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/validationtest/integrations/gomemcache/memcache"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type Integration interface {
	Name() string
	Init(t *testing.T)
	GenSpans(t *testing.T)
	NumSpans() int
}

func TestIntegrations(t *testing.T) {
	ths := []Integration{
		memcachetest.New(),
	}
	for _, th := range ths {
		name := th.Name()
		t.Run(name, func(t *testing.T) {
			sessionToken := fmt.Sprintf("%s-%d", name, time.Now().Unix())
			t.Setenv("CI_TEST_AGENT_SESSION_TOKEN", sessionToken)
			t.Setenv("DD_TRACE_SPAN_ATTRIBUTE_SCHEMA", "v1")

			tracer.Start(tracer.WithAgentAddr("localhost:9126"))
			defer tracer.Stop()

			th.Init(t)
			th.GenSpans(t)

			tracer.Flush()

			assertNumSpans(t, sessionToken, th.NumSpans())
			checkFailures(t, sessionToken)
		})
	}
}

func assertNumSpans(t *testing.T, sessionToken string, numSpans int) {
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

		if len(traces) == 0 {
			return false
		}
		assert.Len(t, traces[0], numSpans)
		return true
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
