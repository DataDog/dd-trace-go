// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package openfeature

import (
	"bytes"
	"cmp"
	"container/list"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/env"
	"github.com/DataDog/dd-trace-go/v2/internal/globalconfig"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	telemetrylog "github.com/DataDog/dd-trace-go/v2/internal/telemetry/log"
	jsoniter "github.com/json-iterator/go"
)

const (
	// defaultExposureFlushInterval is the default interval for flushing exposure events
	// Matches the dd-trace-js implementation (1 second)
	defaultExposureFlushInterval = 1 * time.Second

	// exposureEndpoint is the EVP proxy endpoint for exposure events
	exposureEndpoint = "/evp_proxy/v2/api/v2/exposures"

	// evpSubdomainHeader is the HTTP header name for EVP subdomain routing
	evpSubdomainHeader = "X-Datadog-EVP-Subdomain"

	// evpSubdomainValue is the subdomain value for event platform intake
	evpSubdomainValue = "event-platform-intake"

	// defaultHTTPTimeout is the timeout for HTTP requests to the agent
	defaultHTTPTimeout = 5 * time.Second

	// defaultExposureCacheCapacity is the default capacity for the exposure deduplication cache.
	// 65536 (2^16) provides sufficient capacity for high-throughput production workloads.
	defaultExposureCacheCapacity = 65536
)

// exposureEvent represents a single feature flag evaluation exposure event.
// It matches the schema defined in exposure.json.
type exposureEvent struct {
	Timestamp  int64              `json:"timestamp"`
	Allocation exposureAllocation `json:"allocation"`
	Flag       exposureFlag       `json:"flag"`
	Variant    exposureVariant    `json:"variant"`
	Subject    exposureSubject    `json:"subject"`
}

// exposureAllocation represents allocation information in an exposure event
type exposureAllocation struct {
	Key string `json:"key"`
}

// exposureFlag represents flag information in an exposure event
type exposureFlag struct {
	Key string `json:"key"`
}

// exposureVariant represents variant information in an exposure event
type exposureVariant struct {
	Key string `json:"key"`
}

// exposureSubject represents subject (user/entity) information in an exposure event
type exposureSubject struct {
	ID         string         `json:"id"`
	Type       string         `json:"type,omitempty"`
	Attributes map[string]any `json:"attributes,omitempty"`
}

// exposureContext represents service context metadata for the exposure payload
type exposureContext struct {
	Service string `json:"service"`
	Version string `json:"version,omitempty"`
	Env     string `json:"env,omitempty"`
}

// exposurePayload represents the complete payload sent to the exposure endpoint
type exposurePayload struct {
	Context   exposureContext `json:"context"`
	Exposures []exposureEvent `json:"exposures"`
}

// exposureCacheKey is the key for the exposure deduplication cache
type exposureCacheKey struct {
	flagKey      string
	targetingKey string
}

// exposureCacheValue is the value stored in the exposure deduplication cache
type exposureCacheValue struct {
	allocationKey string
	variant       string
}

// exposureCacheEntry stores the key and value for an LRU cache entry
type exposureCacheEntry struct {
	key   exposureCacheKey
	value exposureCacheValue
}

// exposureLRUCache is a simple LRU cache for exposure deduplication
type exposureLRUCache struct {
	capacity int
	items    map[exposureCacheKey]*list.Element
	order    *list.List // front = most recently used, back = least recently used
}

// newExposureLRUCache creates a new LRU cache with the given capacity
func newExposureLRUCache(capacity int) *exposureLRUCache {
	return &exposureLRUCache{
		capacity: capacity,
		items:    make(map[exposureCacheKey]*list.Element),
		order:    list.New(),
	}
}

// add adds or updates an entry in the cache and returns true if this is a new
// or changed entry (should generate exposure), false if it's a duplicate
func (c *exposureLRUCache) add(key exposureCacheKey, value exposureCacheValue) bool {
	if elem, exists := c.items[key]; exists {
		entry := elem.Value.(*exposureCacheEntry)
		c.order.MoveToFront(elem)
		if entry.value == value {
			// Same allocation and variant - this is a duplicate
			return false
		}
		// Allocation or variant changed - update entry
		entry.value = value
		return true
	}

	// New entry - add to cache
	entry := &exposureCacheEntry{
		key:   key,
		value: value,
	}
	elem := c.order.PushFront(entry)
	c.items[key] = elem

	// Evict if over capacity
	if c.order.Len() > c.capacity {
		c.evictOldest()
	}

	return true
}

// evictOldest removes the least recently used entry
func (c *exposureLRUCache) evictOldest() {
	elem := c.order.Back()
	if elem != nil {
		entry := elem.Value.(*exposureCacheEntry)
		delete(c.items, entry.key)
		c.order.Remove(elem)
	}
}

// exposureWriter manages buffering and flushing of exposure events to the Datadog Agent
type exposureWriter struct {
	mu            sync.Mutex
	buffer        []exposureEvent   // Buffer for exposure events
	cache         *exposureLRUCache // LRU deduplication cache
	flushInterval time.Duration
	httpClient    *http.Client
	agentURL      *url.URL
	context       exposureContext
	ticker        *time.Ticker
	stopChan      chan struct{}
	stopped       bool
	jsonConfig    jsoniter.API
}

// newExposureWriter creates a new exposure writer with the given configuration
func newExposureWriter(config ProviderConfig) *exposureWriter {
	agentURL := internal.AgentURLFromEnv()
	var httpClient *http.Client
	if agentURL.Scheme == "unix" {
		httpClient = internal.UDSClient(agentURL.Path, defaultHTTPTimeout)
		agentURL = internal.UnixDataSocketURL(agentURL.Path)
	} else {
		httpClient = internal.DefaultHTTPClient(defaultHTTPTimeout, false)
	}

	executable, _ := os.Executable()

	return &exposureWriter{
		buffer:        make([]exposureEvent, 0, 1<<8), // Initial capacity of 256
		cache:         newExposureLRUCache(defaultExposureCacheCapacity),
		flushInterval: cmp.Or(config.ExposureFlushInterval, defaultExposureFlushInterval),
		httpClient:    httpClient,
		agentURL:      agentURL,
		stopChan:      make(chan struct{}),
		jsonConfig:    jsoniter.Config{}.Froze(),
		context: exposureContext{
			Service: cmp.Or(env.Get("DD_SERVICE"), globalconfig.ServiceName(), executable),
			Version: env.Get("DD_VERSION"),
			Env:     env.Get("DD_ENV"),
		},
	}
}

// start begins the periodic flushing of exposure events
func (w *exposureWriter) start() {
	w.ticker = time.NewTicker(w.flushInterval)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Error("openfeature: exposure writer recovered panic: %s", r)
				var errAttr slog.Attr
				if err, ok := r.(error); ok {
					errAttr = slog.Any("panic", telemetrylog.NewSafeError(err))
				} else {
					errAttr = slog.Any("panic", r)
				}
				telemetrylog.Error("openfeature: exposure writer recovered panic", errAttr)
			}
			w.stop()
		}()

		for {
			select {
			case <-w.ticker.C:
				w.flush()
			case <-w.stopChan:
				return
			}
		}
	}()
}

// append adds an exposure event to the buffer with deduplication
func (w *exposureWriter) append(event exposureEvent) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.stopped {
		return
	}

	// Create cache key and value from event
	cacheKey := exposureCacheKey{
		flagKey:      event.Flag.Key,
		targetingKey: event.Subject.ID,
	}
	cacheValue := exposureCacheValue{
		allocationKey: event.Allocation.Key,
		variant:       event.Variant.Key,
	}

	// Check deduplication cache - returns true if this is a new or changed entry
	if !w.cache.add(cacheKey, cacheValue) {
		// Duplicate exposure - skip
		return
	}

	// Append event to buffer
	w.buffer = append(w.buffer, event)
}

// flush sends all buffered exposure events to the agent
func (w *exposureWriter) flush() {
	w.mu.Lock()
	if len(w.buffer) == 0 || w.stopped {
		w.mu.Unlock()
		return
	}

	// Move buffer to local variable and create new buffer
	events := w.buffer
	w.buffer = make([]exposureEvent, 0, len(events)/2)
	w.mu.Unlock()

	// Send to agent
	if err := w.sendToAgent(exposurePayload{
		Context:   w.context,
		Exposures: events,
	}); err != nil {
		log.Error("openfeature: failed to send exposure events: %v", err.Error())
	} else {
		log.Debug("openfeature: successfully sent %d exposure events", len(events))
	}
}

// sendToAgent sends the exposure payload to the Datadog Agent via EVP proxy
func (w *exposureWriter) sendToAgent(payload exposurePayload) error {
	// Serialize payload
	var bytesBuffer bytes.Buffer
	encoder := w.jsonConfig.NewEncoder(&bytesBuffer)
	if err := encoder.Encode(payload); err != nil {
		return fmt.Errorf("failed to encode exposure payload: %w", err)
	}

	// Build request URL
	u := *w.agentURL
	u.Path = exposureEndpoint
	requestURL := u.String()

	// Create HTTP request
	req, err := http.NewRequestWithContext(context.Background(), "POST", requestURL, &bytesBuffer)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(evpSubdomainHeader, evpSubdomainValue)

	log.Debug("openfeature: sending exposure events to %s", requestURL)

	// Send request
	resp, err := w.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	// Check response status
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return fmt.Errorf("unexpected status code %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// stop stops the exposure writer and flushes any remaining events
func (w *exposureWriter) stop() {
	w.mu.Lock()
	if w.stopped {
		w.mu.Unlock()
		return
	}
	w.stopped = true
	w.mu.Unlock()

	// Signal the goroutine to stop
	close(w.stopChan)

	// Stop the ticker
	if w.ticker != nil {
		w.ticker.Stop()
	}

	log.Debug("openfeature: exposure writer stopped")
}
