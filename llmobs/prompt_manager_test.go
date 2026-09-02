// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package llmobs

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/open-feature/go-sdk/openfeature"

	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/telemetrytest"
)

func promptResponse(id, version, text string) string {
	encoded, _ := json.Marshal(map[string]any{"prompt_id": id, "version": version, "template": text})
	return string(encoded)
}

func testPromptManager(server *httptest.Server, env string, ttl time.Duration, now func() time.Time, evaluate func(context.Context, string, string, map[string]any) (any, error)) *promptManager {
	return newPromptManager("api", "app", env, server.URL, ttl, false, "", time.Second, server.Client(), now, evaluate)
}

func TestPromptRoutingAndHTTP(t *testing.T) {
	type captured struct {
		method, uri, api, app, language string
		body                            map[string]any
	}
	requests := make(chan captured, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(request.Body).Decode(&body)
		requests <- captured{request.Method, request.RequestURI, request.Header.Get("DD-API-KEY"), request.Header.Get("DD-APPLICATION-KEY"), request.Header.Get("X-Datadog-SDK-Language"), body}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(promptResponse("a/b ?", "3", "hello")))
	}))
	defer server.Close()
	var evaluations atomic.Int32
	manager := testPromptManager(server, "staging", 0, nil, func(context.Context, string, string, map[string]any) (any, error) {
		evaluations.Add(1)
		return map[string]any{}, nil
	})
	version := 3
	prompt, err := manager.get(context.Background(), "a/b ?", getPromptConfig{
		version: &version, targetingKey: "ignored", attributes: map[string]any{"ignored": make(chan int)},
	})
	if err != nil || prompt.Source() != PromptSourceRegistry {
		t.Fatalf("prompt=%#v err=%v", prompt, err)
	}
	exact := <-requests
	if exact.method != http.MethodGet || exact.uri != "/api/unstable/llm-obs/v1/prompts/a%2Fb%20%3F/versions/3" || exact.app != "" || evaluations.Load() != 0 {
		t.Fatalf("exact request %#v evals=%d", exact, evaluations.Load())
	}

	prompt, err = manager.get(context.Background(), "a/b ?", getPromptConfig{targetingKey: "user-1", attributes: map[string]any{"tier": "gold"}})
	if err != nil || prompt.Source() != PromptSourceResolve {
		t.Fatalf("prompt=%#v err=%v", prompt, err)
	}
	resolved := <-requests
	attrs := resolved.body["data"].(map[string]any)["attributes"].(map[string]any)
	if resolved.method != http.MethodPost || resolved.uri != "/api/unstable/llm-obs/v1/prompts/a%2Fb%20%3F/resolve" || resolved.api != "api" || resolved.app != "app" || resolved.language != "go" {
		t.Fatalf("resolve request %#v", resolved)
	}
	if attrs["env"] != "staging" || attrs["targeting_key"] != "user-1" || attrs["context"].(map[string]any)["tier"] != "gold" {
		t.Fatalf("attributes %#v", attrs)
	}

	manager.env = ""
	_, err = manager.get(context.Background(), "a/b ?", getPromptConfig{})
	if err != nil {
		t.Fatal(err)
	}
	latest := <-requests
	if latest.method != http.MethodGet || latest.uri != "/api/unstable/llm-obs/v1/prompts/a%2Fb%20%3F" {
		t.Fatalf("latest %#v", latest)
	}
}

func TestPromptWorksWithoutLLMObsEnabled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, promptResponse("p", "1", "x"))
	}))
	defer server.Close()
	manager := testPromptManager(server, "", 0, nil, nil)
	previous := getGlobalPromptManager
	getGlobalPromptManager = func() (*promptManager, error) { return manager, nil }
	defer func() { getGlobalPromptManager = previous }()
	t.Setenv("DD_LLMOBS_ENABLED", "false")
	prompt, err := GetPrompt(context.Background(), "p")
	if err != nil || prompt.ID() != "p" {
		t.Fatalf("prompt=%#v err=%v", prompt, err)
	}
}

func TestPromptFeatureFlagAndTargeting(t *testing.T) {
	serverCalls := atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		serverCalls.Add(1)
		_, _ = w.Write([]byte(promptResponse("greeting", "http", "http")))
	}))
	defer server.Close()
	seen := make(chan string, 2)
	manager := testPromptManager(server, "staging", time.Minute, nil, func(_ context.Context, key, targetingKey string, attributes map[string]any) (any, error) {
		if key != "__llmobs__.prompt.greeting" || attributes["tier"] != "gold" {
			t.Fatalf("evaluation %q %#v", key, attributes)
		}
		seen <- targetingKey
		return map[string]any{"prompt_id": "greeting", "version": targetingKey, "template": "hello " + targetingKey}, nil
	})
	attributes := map[string]any{"tier": "gold", "targetingKey": "attribute"}
	for _, key := range []string{"alice", "bob"} {
		prompt, err := manager.get(context.Background(), "greeting", getPromptConfig{targetingKey: key, attributes: attributes})
		if err != nil || prompt.Version() != key || prompt.Source() != PromptSourceFeatureFlag {
			t.Fatalf("prompt=%#v err=%v", prompt, err)
		}
	}
	attributes["tier"] = "mutated"
	if <-seen != "alice" || <-seen != "bob" || serverCalls.Load() != 0 {
		t.Fatal("A/B evaluation did not reuse provider seam")
	}
}

type promptABProvider struct{ openfeature.NoopProvider }

func (promptABProvider) ObjectEvaluation(_ context.Context, flag string, _ any, context openfeature.FlattenedContext) openfeature.InterfaceResolutionDetail {
	target, _ := context[openfeature.TargetingKey].(string)
	return openfeature.InterfaceResolutionDetail{Value: map[string]any{"prompt_id": "greeting", "version": target, "template": flag}}
}

func TestPromptUsesRegisteredDefaultProviderForAB(t *testing.T) {
	if err := openfeature.SetProviderAndWait(promptABProvider{}); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = openfeature.SetProviderAndWait(openfeature.NoopProvider{}) }()
	manager := newPromptManager("api", "app", "staging", "https://api.datadoghq.com", time.Minute, false, "", time.Second, nil, nil, nil)
	for _, target := range []string{"alice", "bob"} {
		prompt, err := manager.get(context.Background(), "greeting", getPromptConfig{targetingKey: target, attributes: map[string]any{"targetingKey": "attribute"}})
		if err != nil || prompt.Version() != target || prompt.Template().Text != "__llmobs__.prompt.greeting" {
			t.Fatalf("target=%s prompt=%#v err=%v", target, prompt, err)
		}
	}
}

func TestPromptProviderMissesFallThroughToResolve(t *testing.T) {
	for _, outcome := range []struct {
		name  string
		value any
		err   error
	}{
		{name: "no-op", value: map[string]any{}},
		{name: "missing", value: nil},
		{name: "malformed", value: map[string]any{"prompt_id": "p"}},
		{name: "disabled", err: errors.New("flag disabled")},
		{name: "not ready", err: errors.New("provider not ready")},
	} {
		t.Run(outcome.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				var body struct {
					Data struct {
						Attributes map[string]any `json:"attributes"`
					} `json:"data"`
				}
				if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if len(body.Data.Attributes) != 1 || body.Data.Attributes["env"] != "staging" {
					t.Fatalf("optional fields not omitted: %#v", body)
				}
				_, _ = w.Write([]byte(promptResponse("p", "http", "x")))
			}))
			defer server.Close()
			manager := testPromptManager(server, "staging", 0, nil, func(context.Context, string, string, map[string]any) (any, error) { return outcome.value, outcome.err })
			prompt, err := manager.get(context.Background(), "p", getPromptConfig{})
			if err != nil || prompt.Source() != PromptSourceResolve {
				t.Fatalf("prompt=%#v err=%v", prompt, err)
			}
		})
	}
}

func TestPromptFallbackAuthAndErrors(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		http.Error(w, `{"detail":"boom"}`, http.StatusInternalServerError)
	}))
	defer server.Close()
	manager := testPromptManager(server, "", 0, nil, nil)
	invoked := atomic.Int32{}
	prompt, err := manager.get(context.Background(), "missing", getPromptConfig{fallbackFunc: func() (PromptFallback, error) {
		invoked.Add(1)
		return PromptFallback{Template: PromptTemplate{Text: "local {name}"}, Version: "local-v1"}, nil
	}})
	if err != nil || prompt.Source() != PromptSourceFallback || prompt.Version() != "local-v1" || prompt.Format(map[string]any{"name": "Ada"}).Text != "local Ada" || invoked.Load() != 1 {
		t.Fatalf("prompt=%#v err=%v", prompt, err)
	}
	_, err = manager.get(context.Background(), "missing", getPromptConfig{})
	if err == nil || !strings.Contains(err.Error(), "no fallback was provided") || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("error %v", err)
	}
	fallbackErr := errors.New("fallback failed")
	_, err = manager.get(context.Background(), "missing", getPromptConfig{fallbackFunc: func() (PromptFallback, error) { return PromptFallback{}, fallbackErr }})
	if !errors.Is(err, fallbackErr) {
		t.Fatalf("fallback error %v", err)
	}
	manager.apiKey = ""
	manager.evaluate = func(context.Context, string, string, map[string]any) (any, error) {
		t.Fatal("provider called before auth")
		return nil, nil
	}
	_, err = manager.get(context.Background(), "missing", getPromptConfig{fallback: &PromptFallback{Template: PromptTemplate{Text: "x"}}})
	if !errors.Is(err, ErrPromptAuth) {
		t.Fatalf("auth error %v", err)
	}
	manager.apiKey, manager.env, manager.appKey = "api", "staging", ""
	manager.evaluate = func(context.Context, string, string, map[string]any) (any, error) {
		return nil, errors.New("not ready")
	}
	before := calls.Load()
	prompt, err = manager.get(context.Background(), "missing", getPromptConfig{fallback: &PromptFallback{Template: PromptTemplate{Text: "x"}}})
	if err != nil || prompt.Source() != PromptSourceFallback || calls.Load() != before {
		t.Fatalf("missing app key made HTTP request: %v", err)
	}
}

func TestPromptCacheSelectorsLRUAndFile(t *testing.T) {
	now := time.Unix(100, 0)
	cache := newPromptCache(time.Minute, func() time.Time { return now })
	prompt, _ := newManagedPrompt("p", "1", PromptSourceRegistry, PromptTemplate{Text: "x"}, "", "")
	for i := range promptCacheMaxEntries + 1 {
		cache.set(promptCacheKey{promptID: "p", selector: string(rune(i))}, prompt, now)
	}
	if cache.lru.Len() != promptCacheMaxEntries {
		t.Fatalf("cache size %d", cache.lru.Len())
	}
	if _, _, _, ok := cache.get(promptCacheKey{promptID: "p", selector: string(rune(0))}); ok {
		t.Fatal("LRU did not evict oldest")
	}
	requestA := promptRequest{promptID: "p", env: "staging", targetingKey: "u", attributes: map[string]any{"a": 1, "b": 2}}
	requestB := promptRequest{promptID: "p", env: "staging", targetingKey: "u", attributes: map[string]any{"b": 2, "a": 1}}
	if requestA.cacheKey() != requestB.cacheKey() {
		t.Fatal("attribute order changed selector")
	}
	if requestA.cacheKey() == (promptRequest{promptID: "p"}).cacheKey() {
		t.Fatal("selectors collided")
	}

	dir := t.TempDir()
	files := newPromptFileCache(true, dir, time.Minute, func() time.Time { return now })
	keyA, keyB := promptCacheKey{promptID: "a/b", selector: "latest"}, promptCacheKey{promptID: "a_b", selector: "latest"}
	files.set(keyA, prompt, now.Add(-30*time.Second))
	files.set(keyB, prompt, now)
	if files.path(keyA) == files.path(keyB) {
		t.Fatal("file paths collided")
	}
	_, written, stale, ok := files.get(keyA)
	if !ok || stale || !written.Equal(now.Add(-30*time.Second)) {
		t.Fatalf("file hit ok=%v stale=%v written=%v", ok, stale, written)
	}
	now = now.Add(time.Minute)
	_, _, stale, ok = files.get(keyA)
	if !ok || !stale {
		t.Fatal("file read reset original age")
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(files.path(keyA))
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("file mode %v err=%v", info.Mode(), err)
		}
		dirInfo, err := os.Stat(dir)
		if err != nil || dirInfo.Mode().Perm() != 0o700 {
			t.Fatalf("dir mode %v err=%v", dirInfo.Mode(), err)
		}
	}
	if err := os.WriteFile(files.path(keyA), []byte("bad"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, _, ok := files.get(keyA); ok {
		t.Fatal("corrupt entry was a hit")
	}
}

func TestPromptFreshCacheTTLZeroAndSelectorIsolation(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		count := calls.Add(1)
		_, _ = io.WriteString(w, promptResponse("p", strconv.Itoa(int(count)), "x"))
	}))
	defer server.Close()
	manager := testPromptManager(server, "", time.Minute, nil, nil)
	first, err := manager.get(context.Background(), "p", getPromptConfig{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.get(context.Background(), "p", getPromptConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if first.Version() != "1" || second.Version() != "1" || second.Source() != PromptSourceCache || calls.Load() != 1 {
		t.Fatalf("first=%#v second=%#v calls=%d", first, second, calls.Load())
	}
	if _, err = manager.get(context.Background(), "p", getPromptConfig{fallbackFunc: func() (PromptFallback, error) {
		t.Fatal("fallback called on cache success")
		return PromptFallback{}, nil
	}}); err != nil {
		t.Fatal(err)
	}
	version := 1
	if _, err = manager.get(context.Background(), "p", getPromptConfig{version: &version}); err != nil || calls.Load() != 2 {
		t.Fatalf("version selector err=%v calls=%d", err, calls.Load())
	}

	calls.Store(0)
	manager = testPromptManager(server, "", 0, nil, nil)
	if _, err = manager.get(context.Background(), "p", getPromptConfig{}); err != nil {
		t.Fatal(err)
	}
	if _, err = manager.get(context.Background(), "p", getPromptConfig{}); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatalf("TTL zero calls %d", calls.Load())
	}
}

func TestPromptColdFetchCoalescingAndCancellation(t *testing.T) {
	started, release := make(chan struct{}, 1), make(chan struct{})
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		started <- struct{}{}
		<-release
		_, _ = w.Write([]byte(promptResponse("p", "1", "x")))
	}))
	defer server.Close()
	manager := testPromptManager(server, "", time.Minute, nil, nil)
	results := make(chan error, 2)
	go func() { _, err := manager.get(context.Background(), "p", getPromptConfig{}); results <- err }()
	<-started
	go func() { _, err := manager.get(context.Background(), "p", getPromptConfig{}); results <- err }()
	for range 10 {
		runtime.Gosched()
	}
	close(release)
	if err := <-results; err != nil {
		t.Fatal(err)
	}
	if err := <-results; err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("upstream calls %d", calls.Load())
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := manager.get(canceled, "other", getPromptConfig{fallback: &PromptFallback{Template: PromptTemplate{Text: "x"}}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation %v", err)
	}
}

func TestPromptColdFetchKeepsFallbacksCallerSpecific(t *testing.T) {
	started, release := make(chan struct{}, 1), make(chan struct{})
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		started <- struct{}{}
		<-release
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer server.Close()
	manager := testPromptManager(server, "", time.Minute, nil, nil)
	results := make(chan string, 2)
	call := func(text string) {
		prompt, err := manager.get(context.Background(), "p", getPromptConfig{fallback: &PromptFallback{Template: PromptTemplate{Text: text}}})
		if err != nil {
			results <- err.Error()
			return
		}
		results <- prompt.Template().Text
	}
	go call("first")
	<-started
	go call("second")
	for range 10 {
		runtime.Gosched()
	}
	close(release)
	got := map[string]bool{<-results: true, <-results: true}
	if !got["first"] || !got["second"] || calls.Load() != 1 {
		t.Fatalf("results=%#v calls=%d", got, calls.Load())
	}
}

func TestPromptStaleRefreshEviction(t *testing.T) {
	now := time.Unix(100, 0)
	refreshStarted, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		switch calls.Add(1) {
		case 1:
			_, _ = w.Write([]byte(promptResponse("p", "1", "old")))
		case 2:
			close(refreshStarted)
			<-release
			http.NotFound(w, nil)
		default:
			_, _ = w.Write([]byte(promptResponse("p", "2", "new")))
		}
	}))
	defer server.Close()
	manager := testPromptManager(server, "", time.Second, func() time.Time { return now }, nil)
	first, err := manager.get(context.Background(), "p", getPromptConfig{})
	if err != nil || first.Version() != "1" {
		t.Fatalf("first=%#v err=%v", first, err)
	}
	now = now.Add(2 * time.Second)
	stale, err := manager.get(context.Background(), "p", getPromptConfig{})
	if err != nil || stale.Version() != "1" || stale.Source() != PromptSourceCache {
		t.Fatalf("stale=%#v err=%v", stale, err)
	}
	<-refreshStarted
	close(release)
	for {
		manager.refreshMu.Lock()
		refreshing := len(manager.refreshing)
		manager.refreshMu.Unlock()
		if refreshing == 0 {
			break
		}
		runtime.Gosched()
	}
	refetched, err := manager.get(context.Background(), "p", getPromptConfig{})
	if err != nil || refetched.Version() != "2" || calls.Load() != 3 {
		t.Fatalf("refetched=%#v err=%v calls=%d", refetched, err, calls.Load())
	}
}

func TestPromptStaleRefreshFailurePreservesCache(t *testing.T) {
	base, now := time.Unix(100, 0), time.Unix(100, 0)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			_, _ = io.WriteString(w, promptResponse("p", "1", "old"))
			return
		}
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer server.Close()
	manager := testPromptManager(server, "", time.Second, func() time.Time { return now }, nil)
	if _, err := manager.get(context.Background(), "p", getPromptConfig{}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Second)
	if prompt, err := manager.get(context.Background(), "p", getPromptConfig{}); err != nil || prompt.Version() != "1" {
		t.Fatalf("stale prompt=%#v err=%v", prompt, err)
	}
	for {
		manager.refreshMu.Lock()
		refreshing := len(manager.refreshing)
		manager.refreshMu.Unlock()
		if refreshing == 0 {
			break
		}
		runtime.Gosched()
	}
	now = base
	prompt, err := manager.get(context.Background(), "p", getPromptConfig{})
	if err != nil || prompt.Version() != "1" || calls.Load() != 2 {
		t.Fatalf("preserved prompt=%#v err=%v calls=%d", prompt, err, calls.Load())
	}
}

type promptRoundTripper func(*http.Request) (*http.Response, error)

func (roundTrip promptRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}

func TestPromptTimeoutAndCallerCancellationTelemetry(t *testing.T) {
	recorder := new(telemetrytest.RecordClient)
	defer telemetry.MockClient(recorder)()
	client := &http.Client{Transport: promptRoundTripper(func(request *http.Request) (*http.Response, error) {
		<-request.Context().Done()
		return nil, request.Context().Err()
	})}
	manager := newPromptManager("api", "", "", "https://api.datadoghq.com", 0, false, "", time.Millisecond, client, nil, nil)
	_, err := manager.get(context.Background(), "p", getPromptConfig{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout %v", err)
	}
	if len(recorder.Metrics) == 0 {
		t.Fatal("timeout did not record fetch error")
	}
	recorder.Metrics = nil
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = manager.get(canceled, "p", getPromptConfig{fallback: &PromptFallback{Template: PromptTemplate{Text: "x"}}})
	if !errors.Is(err, context.Canceled) || len(recorder.Metrics) != 0 {
		t.Fatalf("cancel err=%v metrics=%#v", err, recorder.Metrics)
	}
}

func TestPromptResolveIsNotPersisted(t *testing.T) {
	dir := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, promptResponse("p", "1", "x")) }))
	defer server.Close()
	manager := newPromptManager("api", "app", "staging", server.URL, time.Minute, true, dir, time.Second, server.Client(), nil, func(context.Context, string, string, map[string]any) (any, error) { return nil, errors.New("missing") })
	if _, err := manager.get(context.Background(), "p", getPromptConfig{}); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("resolve persisted: %v %#v", err, entries)
	}
	manager.env = ""
	if _, err := manager.get(context.Background(), "p", getPromptConfig{}); err != nil {
		t.Fatal(err)
	}
	entries, err = os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("registry not persisted: %v %#v", err, entries)
	}
}

func TestPromptTelemetry(t *testing.T) {
	recorder := new(telemetrytest.RecordClient)
	defer telemetry.MockClient(recorder)()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { http.NotFound(w, nil) }))
	defer server.Close()
	manager := testPromptManager(server, "", 0, nil, nil)
	_, _ = manager.get(context.Background(), "p", getPromptConfig{fallback: &PromptFallback{Template: PromptTemplate{Text: "x"}}})
	want := map[string]bool{"prompt.fetch.error:error_type:NotFound": false, "prompt.source:from:fallback": false}
	for key := range recorder.Metrics {
		want[key.Name+":"+key.Tags] = true
	}
	for metric, found := range want {
		if !found {
			t.Fatalf("missing telemetry %s: %#v", metric, recorder.Metrics)
		}
	}
}
