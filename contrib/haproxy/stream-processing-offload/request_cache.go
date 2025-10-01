// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package streamprocessingoffload

import (
	"context"
	"fmt"
	"time"

	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/proxy"

	"github.com/jellydator/ttlcache/v3"
	"github.com/negasus/haproxy-spoe-go/message"
)

// initRequestStateCache creates a new cache for request states with a cleanup function that is called when a request state is evicted.
func initRequestStateCache(cleanup func(*proxy.RequestState)) *ttlcache.Cache[uint64, *proxy.RequestState] {
	const requestStateTTL = time.Minute // Default TTL but will be overridden by the timeout value of the HAProxy configuration
	requestStateCache := ttlcache.New[uint64, *proxy.RequestState](
		ttlcache.WithTTL[uint64, *proxy.RequestState](requestStateTTL),
	)

	requestStateCache.OnEviction(func(ctx context.Context, reason ttlcache.EvictionReason, item *ttlcache.Item[uint64, *proxy.RequestState]) {
		cleanup(item.Value())
	})

	go requestStateCache.Start()

	return requestStateCache
}

// getCurrentRequest returns the current request state from the cache based on the `span_id` extracted from the message.
func getCurrentRequest(cache *ttlcache.Cache[uint64, *proxy.RequestState], msg *message.Message) (*proxy.RequestState, error) {
	if cache == nil {
		return nil, fmt.Errorf("the request state cache is not initialized")
	}
	key, err := spanIDFromMessage(msg)
	if err != nil {
		return nil, fmt.Errorf("failed to extract span_id from message: %w", err)
	}

	if item := cache.Get(key); item != nil {
		if v := item.Value(); v != nil {
			return v, nil
		}
	}

	return nil, fmt.Errorf("no current request found for span_id %d", key)
}

// storeRequestState stores the request state in the cache with the given `span_id`.
// The `timeout` string duration value is parsed and used to set the TTL of the cached item.
func storeRequestState(cache *ttlcache.Cache[uint64, *proxy.RequestState], spanId uint64, rs proxy.RequestState, timeout string) {
	timeoutValue, err := time.ParseDuration(timeout)
	if err != nil {
		instr.Logger().Warn("haproxy_spoa: the timeout value '%s' is invalid. Please configure correctly the DD_SPOA_TIMEOUT variable in your HAProxy global configuration. Fallback to 1 minute.", timeout)
		timeoutValue = time.Minute // Fallback to a TTL of 1 minute
	}

	cache.Set(spanId, &rs, timeoutValue)
}
