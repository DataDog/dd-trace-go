// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package fiber

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils"
)

// appSecCommitRules blocks a request with the X-Attack: attack header. A
// second rule blocks a response with status 403, which is the status of the
// first block. A third rule blocks a response with the X-Block: attack header.
// A fourth rule redirects a request with the X-Redirect: attack header, and a
// fifth rule blocks a response with status 303, which is the status of that
// redirect. The rules have different types: with one type, the WAF does not
// report the second match.
const appSecCommitRules = `{
	"version": "2.2",
	"metadata": {"rules_version": "1.0.0"},
	"actions": [{
		"id": "redirect",
		"type": "redirect_request",
		"parameters": {"status_code": 303, "location": "/blocked"}
	}],
	"rules": [{
		"id": "fiber-request-redirect",
		"name": "Redirect request header",
		"tags": {"type": "test-request-redirect", "category": "attack_attempt"},
		"conditions": [{
			"operator": "exact_match",
			"parameters": {
				"inputs": [{"address": "server.request.headers.no_cookies", "key_path": ["x-redirect"]}],
				"list": ["attack"]
			}
		}],
		"on_match": ["redirect"]
	}, {
		"id": "fiber-redirect-status",
		"name": "Block the redirect status",
		"tags": {"type": "test-response-redirect", "category": "attack_attempt"},
		"conditions": [{
			"operator": "exact_match",
			"parameters": {"inputs": [{"address": "server.response.status"}], "list": ["303"]}
		}],
		"on_match": ["block"]
	}, {
		"id": "fiber-request-header",
		"name": "Block request header",
		"tags": {"type": "test-request", "category": "attack_attempt"},
		"conditions": [{
			"operator": "exact_match",
			"parameters": {
				"inputs": [{"address": "server.request.headers.no_cookies", "key_path": ["x-attack"]}],
				"list": ["attack"]
			}
		}],
		"on_match": ["block"]
	}, {
		"id": "fiber-block-status",
		"name": "Block the block status",
		"tags": {"type": "test-response", "category": "attack_attempt"},
		"conditions": [{
			"operator": "exact_match",
			"parameters": {"inputs": [{"address": "server.response.status"}], "list": ["403"]}
		}],
		"on_match": ["block"]
	}, {
		"id": "fiber-response-header",
		"name": "Block response header",
		"tags": {"type": "test-response-header", "category": "attack_attempt"},
		"conditions": [{
			"operator": "exact_match",
			"parameters": {
				"inputs": [{"address": "server.response.headers.no_cookies", "key_path": ["x-block"]}],
				"list": ["attack"]
			}
		}],
		"on_match": ["block"]
	}]
}`

func startAppSecCommitRules(t *testing.T) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "rules.json")
	require.NoError(t, os.WriteFile(path, []byte(appSecCommitRules), 0o600))
	t.Setenv("DD_APPSEC_RULES", path)
	t.Setenv("DD_APPSEC_WAF_TIMEOUT", "1s")
	testutils.StartAppSec(t)
}

// TestAppSecSecondBlockKeepsOneBody blocks a request early. Then a response
// rule matches the status of the block response and blocks again. The client
// must get one block response body, not two. The first block was delivered, so
// the request is reported as blocked.
func TestAppSecSecondBlockKeepsOneBody(t *testing.T) {
	startAppSecCommitRules(t)
	recorder := testutils.StartTelemetryRecorder(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	handlerCalled := false
	router := fiber.New()
	router.Use(Middleware())
	router.Get("/", func(c *fiber.Ctx) error {
		handlerCalled = true
		return c.SendString("handler response")
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Attack", "attack")
	res, err := router.Test(req)
	require.NoError(t, err)
	defer res.Body.Close()

	require.False(t, handlerCalled)
	require.Equal(t, http.StatusForbidden, res.StatusCode)
	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.Equal(t, 1, strings.Count(string(body), `"errors"`), "the response must hold one block payload: %s", body)
	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	appsecJSON, _ := spans[0].Tag("_dd.appsec.json").(string)
	require.Contains(t, appsecJSON, "fiber-request-header")
	require.Contains(t, appsecJSON, "fiber-block-status", "the second block must run")
	requireBlockOutcome(t, recordedMetrics(recorder.Metrics), true)
}

// TestAppSecSecondBlockKeepsBodylessRedirect redirects a POST request early.
// net/http writes no body for the redirect of a POST request. Then a response
// rule matches the redirect status and blocks. The second block must not add
// its body to the redirect response. The client gets the redirect and not the
// block, so the block is reported as failed.
func TestAppSecSecondBlockKeepsBodylessRedirect(t *testing.T) {
	startAppSecCommitRules(t)
	recorder := testutils.StartTelemetryRecorder(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	handlerCalled := false
	router := fiber.New()
	router.Use(Middleware())
	router.Post("/", func(c *fiber.Ctx) error {
		handlerCalled = true
		return c.SendString("handler response")
	})

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("X-Redirect", "attack")
	res, err := router.Test(req)
	require.NoError(t, err)
	defer res.Body.Close()

	require.False(t, handlerCalled)
	require.Equal(t, http.StatusSeeOther, res.StatusCode)
	require.Equal(t, "/blocked", res.Header.Get("Location"))
	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.Empty(t, string(body), "the second block must not add a body to the redirect")
	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	appsecJSON, _ := spans[0].Tag("_dd.appsec.json").(string)
	require.Contains(t, appsecJSON, "fiber-request-redirect")
	require.Contains(t, appsecJSON, "fiber-redirect-status", "the second block must run")
	requireBlockOutcome(t, recordedMetrics(recorder.Metrics), false)
}

// TestAppSecBlockAfterApplicationTimeout makes the handler call
// TimeoutErrorWithCode on the fasthttp context. fasthttp then sends the timeout
// response and discards the live response. A block from a response rule cannot
// reach the client, so AppSec must not report it as delivered.
func TestAppSecBlockAfterApplicationTimeout(t *testing.T) {
	startAppSecCommitRules(t)
	recorder := testutils.StartTelemetryRecorder(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	router := fiber.New()
	router.Use(Middleware())
	router.Get("/", func(c *fiber.Ctx) error {
		c.Set("X-Block", "attack")
		c.Context().TimeoutErrorWithCode("application timeout", http.StatusGatewayTimeout)
		return nil
	})

	res, err := router.Test(httptest.NewRequest(http.MethodGet, "/", nil))
	require.NoError(t, err)
	defer res.Body.Close()

	require.Equal(t, http.StatusGatewayTimeout, res.StatusCode)
	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.Equal(t, "application timeout", string(body))
	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	require.Contains(t, spans[0].Tag("_dd.appsec.json"), "fiber-response-header")
	require.Nil(t, spans[0].Tag("appsec.blocked"), "a block that does not reach the client is not delivered")
	requireBlockOutcome(t, recordedMetrics(recorder.Metrics), false)
}

// recordedMetrics returns the name and the tags of each metric that has a
// value. The telemetry recorder type is internal to dd-trace-go/v2, so this
// function accepts its metrics map through type inference.
func recordedMetrics[K comparable, H interface{ Get() float64 }](metrics map[K]H) []any {
	var keys []any
	for key, handle := range metrics {
		if handle.Get() > 0 {
			keys = append(keys, key)
		}
	}
	return keys
}

// requireBlockOutcome checks the block outcome that the waf.requests metric
// reports. When applied is true, the block was delivered. When applied is
// false, the block failed.
func requireBlockOutcome(t *testing.T, keys []any, applied bool) {
	t.Helper()
	var outcomes []string
	for _, key := range keys {
		v := reflect.ValueOf(key)
		if v.FieldByName("Name").String() != "waf.requests" {
			continue
		}
		for tag := range strings.SplitSeq(v.FieldByName("Tags").String(), ",") {
			if strings.HasPrefix(tag, "request_blocked:") || strings.HasPrefix(tag, "block_failure:") {
				outcomes = append(outcomes, tag)
			}
		}
	}
	want := []string{"request_blocked:false", "block_failure:true"}
	if applied {
		want = []string{"request_blocked:true", "block_failure:false"}
	}
	require.ElementsMatch(t, want, outcomes, "unexpected block outcome")
}
