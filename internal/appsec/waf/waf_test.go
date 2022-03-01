// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

//go:build appsec && cgo && !windows && amd64 && (linux || darwin)
// +build appsec
// +build cgo
// +build !windows
// +build amd64
// +build linux darwin

package waf

import (
	"bytes"
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
	"text/template"
	"time"
	"unsafe"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHealth(t *testing.T) {
	version, err := Health()
	require.NoError(t, err)
	require.NotNil(t, version)
	require.Equal(t, "1.0.18", version.String())
}

var testRule = newTestRule(ruleInput{Address: "server.request.headers.no_cookies", KeyPath: []string{"user-agent"}})

var testRuleTmpl = template.Must(template.New("").Parse(`
{
  "version": "2.1",
  "rules": [
    {
      "id": "ua0-600-12x",
      "name": "Arachni",
      "tags": {
        "type": "security_scanner",
		"category": "attack_attempt"
      },
      "conditions": [
        {
          "operator": "match_regex",
          "parameters": {
            "inputs": [
            {{ range $i, $input := . -}}
              {{ if gt $i 0 }},{{ end }}
                { "address": "{{ $input.Address }}"{{ if ne (len $input.KeyPath) 0 }},  "key_path": [ {{ range $i, $path := $input.KeyPath }}{{ if gt $i 0 }}, {{ end }}"{{ $path }}"{{ end }} ]{{ end }} }
            {{- end }}
            ],
            "regex": "^Arachni"
          }
        }
      ],
      "transformers": []
    }
  ]
}
`))

type ruleInput struct {
	Address string
	KeyPath []string
}

func newTestRule(inputs ...ruleInput) []byte {
	var buf bytes.Buffer
	if err := testRuleTmpl.Execute(&buf, inputs); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

func TestNewWAF(t *testing.T) {
	defer requireZeroNBLiveCObjects(t)
	t.Run("valid-rule", func(t *testing.T) {
		waf, err := NewHandle(testRule)
		require.NoError(t, err)
		require.NotNil(t, waf)
		defer waf.Close()
	})

	t.Run("invalid-json", func(t *testing.T) {
		waf, err := NewHandle([]byte(`not json`))
		require.Error(t, err)
		require.Nil(t, waf)
	})

	t.Run("rule-encoding-error", func(t *testing.T) {
		// For now, the null value cannot be encoded into a WAF object representation so it allows us to cover this
		// case where the JSON rule cannot be encoded into a WAF object.
		waf, err := NewHandle([]byte(`null`))
		require.Error(t, err)
		require.Nil(t, waf)
	})

	t.Run("invalid-rule", func(t *testing.T) {
		// Test with a valid JSON but invalid rule format (field events should be an array)
		const rule = `
{
  "version": "2.1",
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
            "inputs": {
              { "address": "server.request.headers.no_cookies" }
            },
            "regex": "^Arachni"
          }
        }
      ],
      "transformers": []
    }
  ]
}
`
		waf, err := NewHandle([]byte(rule))
		require.Error(t, err)
		require.Nil(t, waf)
	})
}

func TestUsage(t *testing.T) {
	defer requireZeroNBLiveCObjects(t)

	waf, err := NewHandle(newTestRule(ruleInput{Address: "my.input"}))
	require.NoError(t, err)
	require.NotNil(t, waf)

	require.Equal(t, []string{"my.input"}, waf.Addresses())

	wafCtx := NewContext(waf)
	require.NotNil(t, wafCtx)

	// Not matching because the address value doesn't match the rule
	values := map[string]interface{}{
		"my.input": "go client",
	}
	matches, err := wafCtx.Run(values, time.Second)
	require.NoError(t, err)
	require.Nil(t, matches)

	// Not matching because the address is not used by the rule
	values = map[string]interface{}{
		"server.request.uri.raw": "something",
	}
	matches, err = wafCtx.Run(values, time.Second)
	require.NoError(t, err)
	require.Nil(t, matches)

	// Not matching due to a timeout
	values = map[string]interface{}{
		"my.input": "Arachni",
	}
	matches, err = wafCtx.Run(values, 0)
	require.Equal(t, ErrTimeout, err)
	require.Nil(t, matches)

	// Matching
	// Note a WAF rule can only match once. This is why we test the matching case at the end.
	values = map[string]interface{}{
		"my.input": "Arachni",
	}
	matches, err = wafCtx.Run(values, time.Second)
	require.NoError(t, err)
	require.NotEmpty(t, matches)

	// Not matching anymore since it already matched before
	matches, err = wafCtx.Run(values, time.Second)
	require.NoError(t, err)
	require.Nil(t, matches)

	// Nil values
	matches, err = wafCtx.Run(nil, time.Second)
	require.NoError(t, err)
	require.Nil(t, matches)

	// Empty values
	matches, err = wafCtx.Run(map[string]interface{}{}, time.Second)
	require.NoError(t, err)
	require.Nil(t, matches)

	wafCtx.Close()
	waf.Close()
	// Using the WAF instance after it was closed leads to a nil WAF context
	require.Nil(t, NewContext(waf))
}

func TestAddresses(t *testing.T) {
	defer requireZeroNBLiveCObjects(t)
	expectedAddresses := []string{"my.first.input", "my.second.input", "my.third.input", "my.indexed.input"}
	addresses := []ruleInput{{Address: "my.first.input"}, {Address: "my.second.input"}, {Address: "my.third.input"}, {Address: "my.indexed.input", KeyPath: []string{"indexed"}}}
	waf, err := NewHandle(newTestRule(addresses...))
	require.NoError(t, err)
	defer waf.Close()
	require.Equal(t, expectedAddresses, waf.Addresses())
}

func TestConcurrency(t *testing.T) {
	defer requireZeroNBLiveCObjects(t)

	// Start 800 goroutines that will use the WAF 500 times each
	nbUsers := 800
	nbRun := 500

	t.Run("concurrent-waf-release", func(t *testing.T) {
		waf, err := NewHandle(testRule)
		require.NoError(t, err)

		wafCtx := NewContext(waf)
		require.NotNil(t, wafCtx)

		var (
			closed uint32
			done   sync.WaitGroup
		)
		done.Add(1)
		go func() {
			defer done.Done()
			// The implementation currently blocks until the WAF contexts get released
			waf.Close()
			atomic.AddUint32(&closed, 1)
		}()

		// The WAF context is not released so waf.Close() should block and `closed` still be 0
		assert.Equal(t, uint32(0), atomic.LoadUint32(&closed))
		// Release the WAF context, which should unlock the previous waf.Close() call
		wafCtx.Close()
		// Now that the WAF context is closed, wait for the goroutine to close the WAF handle.
		done.Wait()
		require.Equal(t, uint32(1), atomic.LoadUint32(&closed))
	})

	t.Run("concurrent-waf-context-usage", func(t *testing.T) {
		waf, err := NewHandle(testRule)
		require.NoError(t, err)
		defer waf.Close()

		wafCtx := NewContext(waf)
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
					matches, err := wafCtx.Run(data, time.Minute)
					if err != nil {
						panic(err)
						return
					}
					if len(matches) > 0 {
						panic(fmt.Errorf("c=%d matches=`%v`", c, string(matches)))
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
		matches, err := wafCtx.Run(data, time.Second)
		require.NoError(t, err)
		require.NotEmpty(t, matches)
	})

	t.Run("concurrent-waf-instance-usage", func(t *testing.T) {
		waf, err := NewHandle(testRule)
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

				wafCtx := NewContext(waf)
				defer wafCtx.Close()

				for c := 0; c < nbRun; c++ {
					i := c % length
					data := map[string]interface{}{
						"server.request.headers.no_cookies": map[string]string{
							"user-agent": userAgents[i],
						},
					}
					matches, err := wafCtx.Run(data, time.Minute)
					if err != nil {
						panic(err)
					}
					if len(matches) > 0 {
						panic(fmt.Errorf("c=%d matches=`%v`", c, string(matches)))
					}
				}

				// Test the rule matches Arachni in the end
				data := map[string]interface{}{
					"server.request.headers.no_cookies": map[string]string{
						"user-agent": "Arachni",
					},
				}
				matches, err := wafCtx.Run(data, time.Second)
				require.NoError(t, err)
				require.NotEmpty(t, matches)
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

func TestEncoder(t *testing.T) {
	for _, tc := range []struct {
		Name                   string
		Data                   interface{}
		ExpectedError          error
		ExpectedWAFValueType   int
		ExpectedWAFValueLength int
		ExpectedWAFString      string
		MaxValueDepth          interface{}
		MaxArrayLength         interface{}
		MaxMapLength           interface{}
		MaxStringLength        interface{}
	}{
		{
			Name:          "unsupported type",
			Data:          make(chan struct{}),
			ExpectedError: errUnsupportedValue,
		},
		{
			Name:              "string",
			Data:              "hello, waf",
			ExpectedWAFString: "hello, waf",
		},
		{
			Name:                   "string",
			Data:                   "",
			ExpectedWAFValueType:   wafStringType,
			ExpectedWAFValueLength: 0,
		},
		{
			Name:              "byte-slice",
			Data:              []byte("hello, waf"),
			ExpectedWAFString: "hello, waf",
		},
		{
			Name:                   "nil-byte-slice",
			Data:                   []byte(nil),
			ExpectedWAFValueType:   wafStringType,
			ExpectedWAFValueLength: 0,
		},
		{
			Name:                   "map-with-empty-key-string",
			Data:                   map[string]int{"": 1},
			ExpectedWAFValueType:   wafMapType,
			ExpectedWAFValueLength: 1,
		},
		{
			Name:                   "empty-struct",
			Data:                   struct{}{},
			ExpectedWAFValueType:   wafMapType,
			ExpectedWAFValueLength: 0,
		},
		{
			Name: "empty-struct-with-private-fields",
			Data: struct {
				a string
				b int
				c bool
			}{},
			ExpectedWAFValueType:   wafMapType,
			ExpectedWAFValueLength: 0,
		},
		{
			Name:          "nil-interface-value",
			Data:          nil,
			ExpectedError: errUnsupportedValue,
		},
		{
			Name:          "nil-pointer-value",
			Data:          (*string)(nil),
			ExpectedError: errUnsupportedValue,
		},
		{
			Name:          "nil-pointer-value",
			Data:          (*int)(nil),
			ExpectedError: errUnsupportedValue,
		},
		{
			Name:              "non-nil-pointer-value",
			Data:              new(int),
			ExpectedWAFString: "0",
		},
		{
			Name:                   "non-nil-pointer-value",
			Data:                   new(string),
			ExpectedWAFValueType:   wafStringType,
			ExpectedWAFValueLength: 0,
		},
		{
			Name:                   "having-an-empty-map",
			Data:                   map[string]interface{}{},
			ExpectedWAFValueType:   wafMapType,
			ExpectedWAFValueLength: 0,
		},
		{
			Name:          "unsupported",
			Data:          func() {},
			ExpectedError: errUnsupportedValue,
		},
		{
			Name:              "int",
			Data:              int(1234),
			ExpectedWAFString: "1234",
		},
		{
			Name:              "uint",
			Data:              uint(9876),
			ExpectedWAFString: "9876",
		},
		{
			Name:              "bool",
			Data:              true,
			ExpectedWAFString: "true",
		},
		{
			Name:              "bool",
			Data:              false,
			ExpectedWAFString: "false",
		},
		{
			Name:              "float",
			Data:              33.12345,
			ExpectedWAFString: "33",
		},
		{
			Name:              "float",
			Data:              33.62345,
			ExpectedWAFString: "34",
		},
		{
			Name:                   "slice",
			Data:                   []interface{}{33.12345, "ok", 27},
			ExpectedWAFValueType:   wafArrayType,
			ExpectedWAFValueLength: 3,
		},
		{
			Name:                   "slice-having-unsupported-values",
			Data:                   []interface{}{33.12345, func() {}, "ok", 27, nil},
			ExpectedWAFValueType:   wafArrayType,
			ExpectedWAFValueLength: 3,
		},
		{
			Name:                   "array",
			Data:                   [...]interface{}{func() {}, 33.12345, "ok", 27},
			ExpectedWAFValueType:   wafArrayType,
			ExpectedWAFValueLength: 3,
		},
		{
			Name:                   "map",
			Data:                   map[string]interface{}{"k1": 1, "k2": "2"},
			ExpectedWAFValueType:   wafMapType,
			ExpectedWAFValueLength: 2,
		},
		{
			Name:                   "map-with-unsupported-key-values",
			Data:                   map[interface{}]interface{}{"k1": 1, 27: "int key", "k2": "2"},
			ExpectedWAFValueType:   wafMapType,
			ExpectedWAFValueLength: 2,
		},
		{
			Name:                   "map-with-indirect-key-string-values",
			Data:                   map[interface{}]interface{}{"k1": 1, new(string): "string pointer key", "k2": "2"},
			ExpectedWAFValueType:   wafMapType,
			ExpectedWAFValueLength: 3,
		},
		{
			Name: "struct",
			Data: struct {
				Public  string
				private string
				a       string
				A       string
			}{
				Public:  "Public",
				private: "private",
				a:       "a",
				A:       "A",
			},
			ExpectedWAFValueType:   wafMapType,
			ExpectedWAFValueLength: 2, // public fields only
		},
		{
			Name: "struct-with-unsupported-values",
			Data: struct {
				Public  string
				private string
				a       string
				A       func()
			}{
				Public:  "Public",
				private: "private",
				a:       "a",
				A:       nil,
			},
			ExpectedWAFValueType:   wafMapType,
			ExpectedWAFValueLength: 1, // public fields of supported types
		},
		{
			Name:                   "array-max-depth",
			MaxValueDepth:          0,
			Data:                   []interface{}{1, 2, 3, 4, []int{1, 2, 3, 4}},
			ExpectedWAFValueType:   wafArrayType,
			ExpectedWAFValueLength: 4,
		},
		{
			Name:                   "array-max-depth",
			MaxValueDepth:          1,
			Data:                   []interface{}{1, 2, 3, 4, []int{1, 2, 3, 4}},
			ExpectedWAFValueType:   wafArrayType,
			ExpectedWAFValueLength: 5,
		},
		{
			Name:                   "array-max-depth",
			MaxValueDepth:          0,
			Data:                   []interface{}{},
			ExpectedWAFValueType:   wafArrayType,
			ExpectedWAFValueLength: 0,
		},
		{
			Name:                   "map-max-depth",
			MaxValueDepth:          0,
			Data:                   map[string]interface{}{"k1": "v1", "k2": "v2", "k3": "v3", "k4": "v4", "k5": map[string]string{}},
			ExpectedWAFValueType:   wafMapType,
			ExpectedWAFValueLength: 4,
		},
		{
			Name:                   "map-max-depth",
			MaxValueDepth:          1,
			Data:                   map[string]interface{}{"k1": "v1", "k2": "v2", "k3": "v3", "k4": "v4", "k5": map[string]string{}},
			ExpectedWAFValueType:   wafMapType,
			ExpectedWAFValueLength: 5,
		},
		{
			Name:                   "map-max-depth",
			MaxValueDepth:          0,
			Data:                   map[string]interface{}{},
			ExpectedWAFValueType:   wafMapType,
			ExpectedWAFValueLength: 0,
		},
		{
			Name:                   "struct-max-depth",
			MaxValueDepth:          0,
			Data:                   struct{}{},
			ExpectedWAFValueType:   wafMapType,
			ExpectedWAFValueLength: 0,
		},
		{
			Name:          "struct-max-depth",
			MaxValueDepth: 0,
			Data: struct {
				F0 string
				F1 struct{}
			}{},
			ExpectedWAFValueType:   wafMapType,
			ExpectedWAFValueLength: 1,
		},
		{
			Name:          "struct-max-depth",
			MaxValueDepth: 1,
			Data: struct {
				F0 string
				F1 struct{}
			}{},
			ExpectedWAFValueType:   wafMapType,
			ExpectedWAFValueLength: 2,
		},
		{
			Name:              "scalar-values-max-depth-not-accounted",
			MaxValueDepth:     0,
			Data:              -1234,
			ExpectedWAFString: "-1234",
		},
		{
			Name:              "scalar-values-max-depth-not-accounted",
			MaxValueDepth:     0,
			Data:              uint(1234),
			ExpectedWAFString: "1234",
		},
		{
			Name:              "scalar-values-max-depth-not-accounted",
			MaxValueDepth:     0,
			Data:              false,
			ExpectedWAFString: "false",
		},
		{
			Name:                   "array-max-length",
			MaxArrayLength:         3,
			Data:                   []interface{}{1, 2, 3, 4, 5},
			ExpectedWAFValueType:   wafArrayType,
			ExpectedWAFValueLength: 3,
		},
		{
			Name:                   "map-max-length",
			MaxMapLength:           3,
			Data:                   map[string]string{"k1": "v1", "k2": "v2", "k3": "v3", "k4": "v4", "k5": "v5"},
			ExpectedWAFValueType:   wafMapType,
			ExpectedWAFValueLength: 3,
		},
		{
			Name:              "string-max-length",
			MaxStringLength:   3,
			Data:              "123456789",
			ExpectedWAFString: "123",
		},
		{
			Name:                   "string-max-length-truncation-leading-to-same-map-keys",
			MaxStringLength:        1,
			Data:                   map[string]string{"k1": "v1", "k2": "v2", "k3": "v3", "k4": "v4", "k5": "v5"},
			ExpectedWAFValueType:   wafMapType,
			ExpectedWAFValueLength: 5,
		},
		{
			Name:                   "unsupported-array-values",
			MaxArrayLength:         1,
			Data:                   []interface{}{"supported", func() {}, "supported", make(chan struct{})},
			ExpectedWAFValueType:   wafArrayType,
			ExpectedWAFValueLength: 1,
		},
		{
			Name:                   "unsupported-array-values",
			MaxArrayLength:         2,
			Data:                   []interface{}{"supported", func() {}, "supported", make(chan struct{})},
			ExpectedWAFValueType:   wafArrayType,
			ExpectedWAFValueLength: 2,
		},
		{
			Name:                   "unsupported-array-values",
			MaxArrayLength:         1,
			Data:                   []interface{}{func() {}, "supported", make(chan struct{}), "supported"},
			ExpectedWAFValueType:   wafArrayType,
			ExpectedWAFValueLength: 1,
		},
		{
			Name:                   "unsupported-array-values",
			MaxArrayLength:         2,
			Data:                   []interface{}{func() {}, "supported", make(chan struct{}), "supported"},
			ExpectedWAFValueType:   wafArrayType,
			ExpectedWAFValueLength: 2,
		},
		{
			Name:                   "unsupported-array-values",
			MaxArrayLength:         1,
			Data:                   []interface{}{func() {}, make(chan struct{}), "supported"},
			ExpectedWAFValueType:   wafArrayType,
			ExpectedWAFValueLength: 1,
		},
		{
			Name:                   "unsupported-array-values",
			MaxArrayLength:         3,
			Data:                   []interface{}{"supported", func() {}, make(chan struct{})},
			ExpectedWAFValueType:   wafArrayType,
			ExpectedWAFValueLength: 1,
		},
		{
			Name:                   "unsupported-array-values",
			MaxArrayLength:         3,
			Data:                   []interface{}{func() {}, "supported", make(chan struct{})},
			ExpectedWAFValueType:   wafArrayType,
			ExpectedWAFValueLength: 1,
		},
		{
			Name:                   "unsupported-array-values",
			MaxArrayLength:         3,
			Data:                   []interface{}{func() {}, make(chan struct{}), "supported"},
			ExpectedWAFValueType:   wafArrayType,
			ExpectedWAFValueLength: 1,
		},
		{
			Name:                   "unsupported-array-values",
			MaxArrayLength:         2,
			Data:                   []interface{}{func() {}, make(chan struct{}), "supported", "supported"},
			ExpectedWAFValueType:   wafArrayType,
			ExpectedWAFValueLength: 2,
		},
		{
			Name: "unsupported-map-key-types",
			Data: map[interface{}]int{
				"supported":           1,
				interface{ m() }(nil): 1,
				nil:                   1,
				(*int)(nil):           1,
				(*string)(nil):        1,
				make(chan struct{}):   1,
			},
			ExpectedWAFValueType:   wafMapType,
			ExpectedWAFValueLength: 1,
		},
		{
			Name: "unsupported-map-key-types",
			Data: map[interface{}]int{
				interface{ m() }(nil): 1,
				nil:                   1,
				(*int)(nil):           1,
				(*string)(nil):        1,
				make(chan struct{}):   1,
			},
			ExpectedWAFValueType:   wafMapType,
			ExpectedWAFValueLength: 0,
		},
		{
			Name: "unsupported-map-values",
			Data: map[string]interface{}{
				"k0": "supported",
				"k1": func() {},
				"k2": make(chan struct{}),
			},
			MaxMapLength:           3,
			ExpectedWAFValueLength: 1,
		},
		{
			Name: "unsupported-map-values",
			Data: map[string]interface{}{
				"k0": "supported",
				"k1": "supported",
				"k2": make(chan struct{}),
			},
			MaxMapLength:           3,
			ExpectedWAFValueLength: 2,
		},
		{
			Name: "unsupported-map-values",
			Data: map[string]interface{}{
				"k0": "supported",
				"k1": "supported",
				"k2": make(chan struct{}),
			},
			MaxMapLength:           1,
			ExpectedWAFValueLength: 1,
		},
		{
			Name: "unsupported-struct-values",
			Data: struct {
				F0 string
				F1 func()
				F2 chan struct{}
			}{
				F0: "supported",
				F1: func() {},
				F2: make(chan struct{}),
			},
			MaxMapLength:           3,
			ExpectedWAFValueLength: 1,
		},
		{
			Name: "unsupported-map-values",
			Data: struct {
				F0 string
				F1 string
				F2 chan struct{}
			}{
				F0: "supported",
				F1: "supported",
				F2: make(chan struct{}),
			},
			MaxMapLength:           3,
			ExpectedWAFValueLength: 2,
		},
		{
			Name: "unsupported-map-values",
			Data: struct {
				F0 string
				F1 string
				F2 chan struct{}
			}{
				F0: "supported",
				F1: "supported",
				F2: make(chan struct{}),
			},
			MaxMapLength:           1,
			ExpectedWAFValueLength: 1,
		},
	} {
		tc := tc
		t.Run(tc.Name, func(t *testing.T) {
			defer requireZeroNBLiveCObjects(t)

			maxValueDepth := 10
			if max := tc.MaxValueDepth; max != nil {
				maxValueDepth = max.(int)
			}
			maxArrayLength := 1000
			if max := tc.MaxArrayLength; max != nil {
				maxArrayLength = max.(int)
			}
			maxMapLength := 1000
			if max := tc.MaxMapLength; max != nil {
				maxMapLength = max.(int)
			}
			maxStringLength := 4096
			if max := tc.MaxStringLength; max != nil {
				maxStringLength = max.(int)
			}
			e := encoder{
				maxDepth:        maxValueDepth,
				maxStringLength: maxStringLength,
				maxArrayLength:  maxArrayLength,
				maxMapLength:    maxMapLength,
			}
			wo, err := e.encode(tc.Data)
			if tc.ExpectedError != nil {
				require.Error(t, err)
				require.Equal(t, tc.ExpectedError, err)
				require.Nil(t, wo)
				return
			}
			defer free(wo)

			require.NoError(t, err)
			require.NotEqual(t, &wafObject{}, wo)

			if tc.ExpectedWAFValueType != 0 {
				require.Equal(t, tc.ExpectedWAFValueType, int(wo._type), "bad waf value type")
			}
			if tc.ExpectedWAFValueLength != 0 {
				require.Equal(t, tc.ExpectedWAFValueLength, int(wo.nbEntries), "bad waf value length")
			}
			if expectedStr := tc.ExpectedWAFString; expectedStr != "" {
				require.Equal(t, wafStringType, int(wo._type), "bad waf string value type")
				cbuf := uintptr(unsafe.Pointer(*wo.stringValuePtr()))
				gobuf := []byte(expectedStr)
				require.Equal(t, len(gobuf), int(wo.nbEntries), "bad waf value length")
				for i, gobyte := range gobuf {
					// Go pointer arithmetic for cbyte := cbuf[i]
					cbyte := *(*uint8)(unsafe.Pointer(cbuf + uintptr(i)))
					if cbyte != gobyte {
						t.Fatalf("bad waf string value content: i=%d cbyte=%d gobyte=%d", i, cbyte, gobyte)
					}
				}
			}

			// Pass the encoded value to the WAF to make sure it doesn't return an error
			waf, err := NewHandle(newTestRule(ruleInput{Address: "my.input"}))
			require.NoError(t, err)
			defer waf.Close()
			wafCtx := NewContext(waf)
			require.NotNil(t, wafCtx)
			defer wafCtx.Close()
			_, err = wafCtx.Run(map[string]interface{}{
				"my.input": tc.Data,
			}, time.Second)
			require.NoError(t, err)
		})
	}
}

func TestFree(t *testing.T) {
	t.Run("nil-value", func(t *testing.T) {
		require.NotPanics(t, func() {
			free(nil)
		})
	})

	t.Run("zero-value", func(t *testing.T) {
		require.NotPanics(t, func() {
			free(&wafObject{})
		})
	})
}

func BenchmarkEncoder(b *testing.B) {
	defer requireZeroNBLiveCObjects(b)

	rnd := rand.New(rand.NewSource(33))
	buf := make([]byte, 16384)
	n, err := rnd.Read(buf)
	fullstr := string(buf)
	encoder := encoder{
		maxDepth:        10,
		maxStringLength: 1 * 1024 * 1024,
		maxArrayLength:  100,
		maxMapLength:    100,
	}
	for _, l := range []int{1024, 4096, 8192, 16384} {
		b.Run(fmt.Sprintf("%d", l), func(b *testing.B) {
			str := fullstr[:l]
			slice := []string{str, str, str, str, str, str, str, str, str, str}
			data := map[string]interface{}{
				"k0": slice,
				"k1": slice,
				"k2": slice,
				"k3": slice,
				"k4": slice,
				"k5": slice,
				"k6": slice,
				"k7": slice,
				"k8": slice,
				"k9": slice,
			}
			if err != nil || n != len(buf) {
				b.Fatal(err)
			}
			b.ResetTimer()
			for n := 0; n < b.N; n++ {
				v, err := encoder.encode(data)
				if err != nil {
					b.Fatal(err)
				}
				free(v)
			}
		})
	}
}
