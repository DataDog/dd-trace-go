// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package llmobs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/open-feature/go-sdk/openfeature"
	"golang.org/x/sync/singleflight"

	"github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/config"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
)

const promptEndpoint = "/api/unstable/llm-obs/v1/prompts"

type promptManager struct {
	apiKey, appKey, env, origin string
	timeout                     time.Duration
	cacheEnabled                bool
	cache                       *promptCache
	fileCache                   *promptFileCache
	httpClient                  *http.Client
	now                         func() time.Time
	evaluate                    func(context.Context, string, string, map[string]any) (any, error)
	fills                       singleflight.Group
	refreshMu                   sync.Mutex
	refreshing                  map[promptCacheKey]struct{}
}

type promptRequest struct {
	promptID     string
	version      *int
	env          string
	targetingKey string
	attributes   map[string]any
}

func (request promptRequest) source() PromptSource {
	if request.env != "" && request.version == nil {
		return PromptSourceResolve
	}
	return PromptSourceRegistry
}

func (request promptRequest) cacheKey() promptCacheKey {
	selector := struct {
		Kind         string         `json:"kind"`
		Version      *int           `json:"version,omitempty"`
		Env          string         `json:"env,omitempty"`
		TargetingKey string         `json:"targeting_key,omitempty"`
		Attributes   map[string]any `json:"attributes,omitempty"`
	}{}
	switch {
	case request.version != nil:
		selector.Kind, selector.Version = "version", request.version
	case request.env != "":
		selector.Kind, selector.Env, selector.TargetingKey, selector.Attributes = "resolve", request.env, request.targetingKey, request.attributes
	default:
		selector.Kind = "latest"
	}
	encoded, _ := json.Marshal(selector)
	return promptCacheKey{promptID: request.promptID, selector: string(encoded)}
}

var globalPromptManagerState struct {
	sync.Mutex
	config  *config.Config
	manager *promptManager
}

var getGlobalPromptManager = func() *promptManager {
	cfg := config.Get()
	env := cfg.Env()
	globalPromptManagerState.Lock()
	defer globalPromptManagerState.Unlock()
	if globalPromptManagerState.config != cfg || globalPromptManagerState.manager.env != env {
		globalPromptManagerState.config = cfg
		globalPromptManagerState.manager = newPromptManager(
			cfg.APIKey(), cfg.AppKey(), env, "https://api."+cfg.Site(),
			cfg.LLMObsPromptsCacheTTL(), cfg.LLMObsPromptsFileCacheEnabled(), cfg.LLMObsPromptsCacheDir(),
			cfg.LLMObsPromptsTimeout(), nil, nil, nil,
		)
	}
	return globalPromptManagerState.manager
}

func globalPromptManager() *promptManager {
	return getGlobalPromptManager()
}

func newPromptManager(apiKey, appKey, env, origin string, ttl time.Duration, fileCacheEnabled bool, cacheDir string, timeout time.Duration, client *http.Client, now func() time.Time, evaluate func(context.Context, string, string, map[string]any) (any, error)) *promptManager {
	if now == nil {
		now = time.Now
	}
	if client == nil {
		client = internal.DefaultHTTPClient(timeout, true)
	}
	if evaluate == nil {
		evaluate = func(ctx context.Context, key, targetingKey string, attributes map[string]any) (any, error) {
			details, err := openfeature.NewDefaultClient().ObjectValueDetails(ctx, key, map[string]any{}, openfeature.NewEvaluationContext(targetingKey, attributes))
			return details.Value, err
		}
	}
	return &promptManager{
		apiKey: apiKey, appKey: appKey, env: env, origin: origin, timeout: timeout,
		cacheEnabled: ttl > 0, cache: newPromptCache(ttl, now), fileCache: newPromptFileCache(fileCacheEnabled && ttl > 0, cacheDir, ttl, now),
		httpClient: client, now: now, evaluate: evaluate, refreshing: make(map[promptCacheKey]struct{}),
	}
}

func (manager *promptManager) get(ctx context.Context, promptID string, options getPromptConfig) (*ManagedPrompt, error) {
	if manager.apiKey == "" {
		return nil, ErrPromptAuth
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var attributes map[string]any
	if options.version == nil && manager.env != "" {
		attributes = maps.Clone(options.attributes)
		if _, err := json.Marshal(attributes); err != nil {
			return nil, fmt.Errorf("llmobs: invalid prompt targeting attributes: %w", err)
		}
	}
	if options.version == nil && manager.env != "" {
		value, err := manager.evaluate(ctx, "__llmobs__.prompt."+promptID, options.targetingKey, attributes)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if err == nil {
			if prompt, parseErr := parsePrompt(value, PromptSourceFeatureFlag); parseErr == nil {
				recordPromptSource("ff")
				return prompt, nil
			}
		}
	}
	request := promptRequest{promptID: promptID, version: options.version}
	if options.version == nil && manager.env != "" {
		request.env, request.targetingKey, request.attributes = manager.env, options.targetingKey, attributes
	}
	prompt, err := manager.getHTTP(ctx, request)
	if err == nil {
		return prompt, nil
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	fallback := options.fallback
	if options.fallbackFunc != nil {
		value, fallbackErr := options.fallbackFunc()
		if fallbackErr != nil {
			return nil, fallbackErr
		}
		fallback = &value
	}
	if fallback == nil {
		message := fmt.Sprintf("Prompt '%s' could not be fetched and no fallback was provided", promptID)
		if err.Error() == "" {
			return nil, errors.New(message)
		}
		return nil, fmt.Errorf("%s: %w", message, err)
	}
	version := fallback.Version
	if version == "" {
		version = "fallback"
	}
	prompt, fallbackErr := newManagedPrompt(promptID, version, PromptSourceFallback, fallback.Template, "", "")
	if fallbackErr != nil {
		return nil, fallbackErr
	}
	recordPromptSource("fallback")
	return prompt, nil
}

func (manager *promptManager) getHTTP(ctx context.Context, request promptRequest) (*ManagedPrompt, error) {
	key := request.cacheKey()
	if manager.cacheEnabled {
		if prompt, _, stale, ok := manager.cache.get(key); ok {
			if stale {
				manager.refresh(ctx, request)
			}
			recordPromptSource("hot_cache")
			return prompt, nil
		}
		if request.source() == PromptSourceRegistry {
			if prompt, writtenAt, stale, ok := manager.fileCache.get(key); ok {
				manager.cache.set(key, prompt, writtenAt)
				if stale {
					manager.refresh(ctx, request)
				}
				recordPromptSource("warm_cache")
				return prompt, nil
			}
		}
	}
	result := manager.fills.DoChan(key.promptID+"\x00"+key.selector, func() (any, error) {
		shared, cancel := manager.sharedContext(ctx)
		defer cancel()
		return manager.fetchAndCache(shared, request, false)
	})
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case result := <-result:
		if result.Err != nil {
			return nil, result.Err
		}
		recordPromptSource(string(request.source()))
		return result.Val.(*ManagedPrompt), nil
	}
}

func (manager *promptManager) sharedContext(ctx context.Context) (context.Context, context.CancelFunc) {
	shared := context.WithoutCancel(ctx)
	if manager.timeout == 0 {
		return context.WithCancel(shared)
	}
	return context.WithTimeout(shared, manager.timeout)
}

func (manager *promptManager) refresh(ctx context.Context, request promptRequest) {
	key := request.cacheKey()
	manager.refreshMu.Lock()
	if _, exists := manager.refreshing[key]; exists {
		manager.refreshMu.Unlock()
		return
	}
	manager.refreshing[key] = struct{}{}
	manager.refreshMu.Unlock()
	go func() {
		defer func() {
			manager.refreshMu.Lock()
			delete(manager.refreshing, key)
			manager.refreshMu.Unlock()
		}()
		shared, cancel := manager.sharedContext(ctx)
		defer cancel()
		_, _ = manager.fetchAndCache(shared, request, true)
	}()
}

type promptFetchError struct {
	reason   string
	notFound bool
	cause    error
}

func (err *promptFetchError) Error() string { return err.reason }
func (err *promptFetchError) Unwrap() error { return err.cause }

func (manager *promptManager) fetchAndCache(ctx context.Context, request promptRequest, evictNotFound bool) (*ManagedPrompt, error) {
	prompt, err := manager.fetchHTTP(ctx, request)
	if err != nil {
		if err.notFound {
			recordPromptFetchError("NotFound")
			if evictNotFound {
				manager.cache.delete(request.cacheKey())
				manager.fileCache.delete(request.cacheKey())
			}
		} else {
			recordPromptFetchError("FetchError")
		}
		return nil, err
	}
	if manager.cacheEnabled {
		writtenAt := manager.now()
		manager.cache.set(request.cacheKey(), prompt, writtenAt)
		if request.source() == PromptSourceRegistry {
			manager.fileCache.set(request.cacheKey(), prompt, writtenAt)
		}
	}
	return prompt, nil
}

func (manager *promptManager) fetchHTTP(ctx context.Context, request promptRequest) (*ManagedPrompt, *promptFetchError) {
	if request.source() == PromptSourceResolve && manager.appKey == "" {
		return nil, &promptFetchError{reason: "an application key is required to resolve prompts for an environment"}
	}
	escapedID := url.PathEscape(request.promptID)
	path, method := promptEndpoint+"/"+escapedID, http.MethodGet
	var body io.Reader
	if request.version != nil {
		path += "/versions/" + strconv.Itoa(*request.version)
	} else if request.env != "" {
		path, method = path+"/resolve", http.MethodPost
		attributes := map[string]any{"env": request.env}
		if request.targetingKey != "" {
			attributes["targeting_key"] = request.targetingKey
		}
		if len(request.attributes) != 0 {
			attributes["context"] = request.attributes
		}
		encoded, _ := json.Marshal(map[string]any{"data": map[string]any{"type": "prompt_resolve_requests", "attributes": attributes}})
		body = bytes.NewReader(encoded)
	}
	httpRequest, err := http.NewRequestWithContext(ctx, method, manager.origin+path, body)
	if err != nil {
		return nil, &promptFetchError{reason: err.Error(), cause: err}
	}
	httpRequest.Header.Set("DD-API-KEY", manager.apiKey)
	httpRequest.Header.Set("X-Datadog-SDK-Language", "go")
	if method == http.MethodPost {
		httpRequest.Header.Set("DD-APPLICATION-KEY", manager.appKey)
		httpRequest.Header.Set("Content-Type", "application/json")
	}
	response, err := manager.httpClient.Do(httpRequest)
	if err != nil {
		log.Warn("Prompt fetch exception: prompt_id=%s: %v", request.promptID, err.Error())
		return nil, &promptFetchError{reason: err.Error(), cause: err}
	}
	defer response.Body.Close()
	data, readErr := io.ReadAll(response.Body)
	if readErr != nil {
		return nil, &promptFetchError{reason: readErr.Error(), cause: readErr}
	}
	if response.StatusCode != http.StatusOK {
		reason := promptErrorDetail(data)
		notFound := response.StatusCode == http.StatusNotFound
		if notFound {
			log.Debug("Prompt not found: prompt_id=%s detail=%q", request.promptID, reason)
		} else {
			log.Warn("Prompt fetch failed: prompt_id=%s status=%d detail=%q", request.promptID, response.StatusCode, reason)
		}
		return nil, &promptFetchError{reason: reason, notFound: notFound}
	}
	prompt, parseErr := parsePrompt(data, request.source())
	if parseErr != nil {
		return nil, &promptFetchError{reason: parseErr.Error(), cause: parseErr}
	}
	return prompt, nil
}

func parsePrompt(raw any, source PromptSource) (*ManagedPrompt, error) {
	var encoded []byte
	switch value := raw.(type) {
	case []byte:
		encoded = value
	default:
		var err error
		encoded, err = json.Marshal(value)
		if err != nil {
			return nil, err
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	var data map[string]any
	if err := decoder.Decode(&data); err != nil || len(data) == 0 {
		if err == nil {
			err = errors.New("empty prompt object")
		}
		return nil, fmt.Errorf("invalid prompt response: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, errors.New("invalid prompt response: trailing JSON data")
	}
	id, _ := data["prompt_id"].(string)
	if id == "" {
		return nil, errors.New("invalid prompt response: missing prompt_id")
	}
	version := promptVersion(data["user_version"])
	if version == "" {
		version = promptVersion(data["version"])
	}
	if version == "" || version == "0" {
		return nil, errors.New("invalid prompt response: missing version")
	}
	template, err := promptTemplate(data)
	if err != nil {
		return nil, err
	}
	promptUUID, _ := data["prompt_uuid"].(string)
	versionUUID, _ := data["prompt_version_uuid"].(string)
	if versionUUID == "" {
		versionUUID, _ = data["id"].(string)
	}
	if versionUUID == "" {
		versionUUID, _ = data["ID"].(string)
	}
	return newManagedPrompt(id, version, source, template, promptUUID, versionUUID)
}

func promptVersion(value any) string {
	switch value := value.(type) {
	case string:
		return value
	case json.Number:
		if number, err := value.Float64(); err != nil || number == 0 {
			return ""
		}
		return value.String()
	case bool:
		if !value {
			return ""
		}
		return "true"
	case nil:
		return ""
	default:
		return fmt.Sprint(value)
	}
}

func promptTemplate(data map[string]any) (PromptTemplate, error) {
	value, exists := data["template"]
	if text, ok := value.(string); ok && text != "" && data["chat_template"] != nil {
		return PromptTemplate{}, errors.New("invalid prompt response: template cannot contain both text and messages")
	}
	if !exists || value == nil {
		value, exists = data["chat_template"]
	}
	if !exists || value == nil {
		return PromptTemplate{Messages: []PromptMessage{}}, nil
	}
	if text, ok := value.(string); ok {
		return PromptTemplate{Text: text}, nil
	}
	items, ok := value.([]any)
	if !ok {
		return PromptTemplate{}, errors.New("invalid prompt response: template must be text or messages")
	}
	messages := make([]PromptMessage, len(items))
	for i, item := range items {
		message, ok := item.(map[string]any)
		if !ok {
			return PromptTemplate{}, errors.New("invalid prompt response: invalid chat message")
		}
		role, roleOK := message["role"].(string)
		content, contentOK := message["content"].(string)
		if !roleOK || !contentOK {
			return PromptTemplate{}, errors.New("invalid prompt response: chat role and content must be strings")
		}
		messages[i] = PromptMessage{Role: role, Content: content}
	}
	return PromptTemplate{Messages: messages}, nil
}

func promptErrorDetail(data []byte) string {
	var response struct {
		Detail string `json:"detail"`
	}
	if json.Unmarshal(data, &response) == nil && response.Detail != "" {
		return response.Detail
	}
	return strings.TrimSpace(string(data))
}

func recordPromptSource(source string) {
	telemetry.Count(telemetry.NamespaceMLObs, "prompt.source", []string{"from:" + source}).Submit(1)
}

func recordPromptFetchError(errorType string) {
	telemetry.Count(telemetry.NamespaceMLObs, "prompt.fetch.error", []string{"error_type:" + errorType}).Submit(1)
}
