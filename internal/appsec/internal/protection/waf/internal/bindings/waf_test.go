// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

//go:build !without_ddwaf && cgo && !windows && amd64 && (linux || darwin)
// +build !without_ddwaf
// +build cgo
// +build !windows
// +build amd64
// +build linux darwin

package bindings

import (
	"bytes"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"text/template"
	"time"

	"github.com/stretchr/testify/require"
)

func TestHealth(t *testing.T) {
	version, err := Health()
	require.NoError(t, err)
	require.NotNil(t, version)
	require.Equal(t, "1.0.12", version.String())
}

var testRule = newTestRule("server.request.headers.no_cookies:user-agent")

var testRuleTmpl = template.Must(template.New("").Parse(`
{
  "version": "1.0",
  "events": [
    {
      "id": "ua0-600-12x",
      "name": "Arachni",
      "tags": {
        "type": "security_scanner"
      },
      "conditions": [
        {
          "operation": "match_regex",
          "parameters": {
            "inputs": [
              "{{ .Input }}"
            ],
            "regex": "^Arachni"
          }
        }
      ],
      "transformers": [],
      "action": "record"
    }
  ]
}
`))

func newTestRule(input string) []byte {
	var buf bytes.Buffer
	if err := testRuleTmpl.Execute(&buf, struct {
		Input string
	}{input}); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

func TestNewWAF(t *testing.T) {
	defer requireZeroNBLiveCObjects(t)
	t.Run("valid-rule", func(t *testing.T) {
		waf, err := NewWAF(testRule)
		require.NoError(t, err)
		require.NotNil(t, waf)
		defer waf.Close()
	})

	t.Run("invalid-json", func(t *testing.T) {
		waf, err := NewWAF([]byte(`not json`))
		require.Error(t, err)
		require.Nil(t, waf)
	})

	t.Run("rule-encoding-error", func(t *testing.T) {
		// For now, the null value cannot be encoded into a WAF object representation so it allows us to cover this
		// case where the JSON rule cannot be encoded into a WAF object.
		waf, err := NewWAF([]byte(`null`))
		require.Error(t, err)
		require.Nil(t, waf)
	})

	t.Run("invalid-rule", func(t *testing.T) {
		// Test with a valid JSON but invalid rule format (field events should be an array)
		const rule = `
{
  "version": "1.0",
  "events": {
    {
      "id": "ua0-600-12x",
      "name": "Arachni",
      "tags": {
        "type": "security_scanner"
      },
      "conditions": [
        {
          "operation": "match_regex",
          "parameters": {
            "inputs": [
              "server.request.headers.no_cookies:user-agent"
            ],
            "regex": "^Arachni"
          }
        }
      ],
      "transformers": [],
      "action": "record"
    }
  }
}
`
		waf, err := NewWAF([]byte(rule))
		require.Error(t, err)
		require.Nil(t, waf)
	})
}

func TestUsage(t *testing.T) {
	defer requireZeroNBLiveCObjects(t)

	waf, err := NewWAF(testRule)
	require.NoError(t, err)
	require.NotNil(t, waf)

	wafCtx := NewWAFContext(waf)
	require.NotNil(t, wafCtx)

	// Not matching
	values := map[string]interface{}{
		"server.request.headers.no_cookies": map[string][]string{
			"user-agent": {"go client"},
		},
	}
	action, md, err := wafCtx.Run(values, time.Second)
	require.NoError(t, err)
	require.Equal(t, NoAction, action)
	require.Nil(t, md)

	// Rule address not available
	values = map[string]interface{}{
		"server.request.uri.raw": "something",
	}
	action, md, err = wafCtx.Run(values, time.Second)
	require.NoError(t, err)
	require.Equal(t, NoAction, action)
	require.Nil(t, md)

	// Timeout
	values = map[string]interface{}{
		"server.request.headers.no_cookies": map[string][]string{
			"user-agent": {"Arachni"},
		},
	}
	action, md, err = wafCtx.Run(values, 0)
	require.Equal(t, ErrTimeout, err)
	require.Equal(t, NoAction, action)
	require.Empty(t, md)

	// Not matching anymore since it already matched before
	values = map[string]interface{}{
		"server.request.headers.no_cookies": map[string][]string{
			"user-agent": {"Arachni"},
		},
	}
	action, md, err = wafCtx.Run(values, 0)
	require.Equal(t, ErrTimeout, err)
	require.Equal(t, NoAction, action)
	require.Empty(t, md)

	// Matching
	// Note a WAF rule can only match once. This is why we test the matching case at the end.
	values = map[string]interface{}{
		"server.request.headers.no_cookies": map[string][]string{
			"user-agent": {"Arachni"},
		},
	}
	action, md, err = wafCtx.Run(values, time.Second)
	require.NoError(t, err)
	require.Equal(t, MonitorAction, action)
	require.NotEmpty(t, md)

	// Not matching anymore since it already matched before
	values = map[string]interface{}{
		"server.request.headers.no_cookies": map[string][]string{
			"user-agent": {"Arachni"},
		},
	}
	action, md, err = wafCtx.Run(values, 0)
	require.Equal(t, ErrTimeout, err)
	require.Equal(t, NoAction, action)
	require.Empty(t, md)

	// Nil values
	action, md, err = wafCtx.Run(nil, time.Second)
	require.NoError(t, err)
	require.Equal(t, NoAction, action)
	require.Empty(t, md)

	// Empty values
	action, md, err = wafCtx.Run(map[string]interface{}{}, time.Second)
	require.NoError(t, err)
	require.Equal(t, NoAction, action)
	require.Empty(t, md)

	wafCtx.Close()
	waf.Close()
	// Using the WAF instance after it was closed leads to a nil WAF context
	require.Nil(t, NewWAFContext(waf))
}

func TestConcurrency(t *testing.T) {
	defer requireZeroNBLiveCObjects(t)

	// Start 2000 goroutines that will use the WAF 1000 times each
	nbUsers := 2000
	nbRun := 1000

	t.Run("concurrent-waf-release", func(t *testing.T) {
		t.Parallel()

		waf, err := NewWAF(testRule)
		require.NoError(t, err)

		wafCtx := NewWAFContext(waf)
		require.NotNil(t, wafCtx)

		var (
			startBarrier sync.WaitGroup
			called       uint32
		)
		startBarrier.Add(1)
		go func() {
			startBarrier.Wait()
			wafCtx.Close()
			atomic.AddUint32(&called, 1)
		}()

		// The implementation currently blocks until the WAF contexts get released
		startBarrier.Done()
		require.Equal(t, uint32(0), atomic.LoadUint32(&called))
		waf.Close()
		require.Equal(t, uint32(1), atomic.LoadUint32(&called))
	})

	t.Run("concurrent-waf-context-usage", func(t *testing.T) {
		t.Parallel()

		waf, err := NewWAF(testRule)
		require.NoError(t, err)
		defer waf.Close()

		wafCtx := NewWAFContext(waf)
		defer wafCtx.Close()

		// User agents that won't match the rule so that it doesn't get pruned.
		// Said otherwise, the User-Agent rule will run as long as it doesn't match, otherwise it gets ignored.
		// This is the reason why the following user agent are not Arachni.
		userAgents := [...]string{"Foo", "Bar", "Datadog"}
		length := len(userAgents)

		var startBarrier, stopBarrier sync.WaitGroup
		// Create a start barrier to synchronize every goroutine's launch and
		// increase the chances of parallel accesses
		startBarrier.Add(1)
		// Create a stopBarrier to signal when all user goroutines are done.
		stopBarrier.Add(nbUsers)

		for n := 0; n < nbUsers; n++ {
			go func() {
				startBarrier.Wait()      // Sync the starts of the goroutines
				defer stopBarrier.Done() // Signal we are done when returning

				for c := 0; c < nbRun; c++ {
					i := c % length
					data := map[string]interface{}{
						"server.request.headers.no_cookies": map[string]string{
							"user-agent": userAgents[i],
						},
					}
					action, match, err := wafCtx.Run(data, time.Minute)
					if err != nil {
						panic(err)
						return
					}
					if action != NoAction || len(match) > 0 {
						panic(fmt.Errorf("c=%d action=`%v` match=`%v`", c, action, string(match)))
					}
				}
			}()
		}

		// Save the test start time to compare it to the first metrics store's
		// that should be latter.
		startBarrier.Done() // Unblock the user goroutines
		stopBarrier.Wait()  // Wait for the user goroutines to be done

		// Test the rule matches Arachni in the end
		data := map[string]interface{}{
			"server.request.headers.no_cookies": map[string]string{
				"user-agent": "Arachni",
			},
		}
		action, _, _ := wafCtx.Run(data, time.Second)
		require.Equal(t, MonitorAction, action)
	})

	t.Run("concurrent-waf-instance-usage", func(t *testing.T) {
		t.Parallel()

		waf, err := NewWAF(testRule)
		require.NoError(t, err)
		defer waf.Close()

		// User agents that won't match the rule so that it doesn't get pruned.
		// Said otherwise, the User-Agent rule will run as long as it doesn't match, otherwise it gets ignored.
		// This is the reason why the following user agent are not Arachni.
		userAgents := [...]string{"Foo", "Bar", "Datadog"}
		length := len(userAgents)

		var startBarrier, stopBarrier sync.WaitGroup
		// Create a start barrier to synchronize every goroutine's launch and
		// increase the chances of parallel accesses
		startBarrier.Add(1)
		// Create a stopBarrier to signal when all user goroutines are done.
		stopBarrier.Add(nbUsers)

		for n := 0; n < nbUsers; n++ {
			go func() {
				startBarrier.Wait()      // Sync the starts of the goroutines
				defer stopBarrier.Done() // Signal we are done when returning

				wafCtx := NewWAFContext(waf)
				defer wafCtx.Close()

				for c := 0; c < nbRun; c++ {
					i := c % length
					data := map[string]interface{}{
						"server.request.headers.no_cookies": map[string]string{
							"user-agent": userAgents[i],
						},
					}
					action, match, err := wafCtx.Run(data, time.Minute)
					if err != nil {
						panic(err)
					}
					if action != NoAction || len(match) > 0 {
						panic(fmt.Errorf("c=%d action=`%v` match=`%v`", c, action, string(match)))
					}
				}

				// Test the rule matches Arachni in the end
				data := map[string]interface{}{
					"server.request.headers.no_cookies": map[string]string{
						"user-agent": "Arachni",
					},
				}
				action, _, _ := wafCtx.Run(data, time.Second)
				require.Equal(t, MonitorAction, action)
			}()
		}

		// Save the test start time to compare it to the first metrics store's
		// that should be latter.
		startBarrier.Done() // Unblock the user goroutines
		stopBarrier.Wait()  // Wait for the user goroutines to be done
	})
}

func TestRunError(t *testing.T) {
	for _, tc := range []struct {
		Err            error
		ExpectedString string
	}{
		{
			Err:            ErrInternal,
			ExpectedString: "internal waf error",
		},
		{
			Err:            ErrTimeout,
			ExpectedString: "waf timeout",
		},
		{
			Err:            ErrInvalidObject,
			ExpectedString: "invalid waf object",
		},
		{
			Err:            ErrInvalidArgument,
			ExpectedString: "invalid waf argument",
		},
		{
			Err:            ErrOutOfMemory,
			ExpectedString: "out of memory",
		},
		{
			Err:            RunError(33),
			ExpectedString: "unknown waf error 33",
		},
	} {
		t.Run(tc.ExpectedString, func(t *testing.T) {
			require.Equal(t, tc.ExpectedString, tc.Err.Error())
		})
	}
}

func requireZeroNBLiveCObjects(t testing.TB) {
	require.Equal(t, uint64(0), atomic.LoadUint64(&nbLiveCObjects))
}
