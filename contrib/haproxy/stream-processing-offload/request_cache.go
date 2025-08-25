package streamprocessingoffload

import (
	"context"
	"fmt"
	"github.com/DataDog/dd-trace-go/contrib/envoyproxy/go-control-plane/v2/message_processor"
	"github.com/jellydator/ttlcache/v3"
	"github.com/negasus/haproxy-spoe-go/message"
	"time"
)

func initRequestStateCache(cleanup func(*message_processor.RequestState)) *ttlcache.Cache[uint64, *message_processor.RequestState] {
	const requestStateTTL = time.Minute // Default TTL but will be overridden by the timeout value of the HAProxy configuration
	requestStateCache := ttlcache.New[uint64, *message_processor.RequestState](
		ttlcache.WithTTL[uint64, *message_processor.RequestState](requestStateTTL),
	)

	requestStateCache.OnEviction(func(ctx context.Context, reason ttlcache.EvictionReason, item *ttlcache.Item[uint64, *message_processor.RequestState]) {
		cleanup(item.Value())
	})

	go requestStateCache.Start()

	return requestStateCache
}

func getCurrentRequest(cache *ttlcache.Cache[uint64, *message_processor.RequestState], msg *message.Message) (*message_processor.RequestState, error) {
	if cache == nil {
		return nil, fmt.Errorf("requestStateCache is not initialized")
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

func storeCurrentRequest(cache *ttlcache.Cache[uint64, *message_processor.RequestState], spanId uint64, rs message_processor.RequestState, timeout string) {
	timeoutValue, err := time.ParseDuration(timeout)
	if err != nil {
		instr.Logger().Warn("haproxy_spoa: the timeout value '%s' is invalid. Please configure correctly the DD_SPOA_TIMEOUT variable in your HAProxy global configuration. Fallback to 1 minute.", timeout)
		timeoutValue = time.Minute // Fallback to a TTL of 1 minute
	}

	local := rs
	cache.Set(spanId, &local, timeoutValue)
}

// deleteCurrentRequest removes a RequestState from the cache; call this at end of request lifecycle
func deleteCurrentRequest(cache *ttlcache.Cache[uint64, *message_processor.RequestState], spanId uint64) {
	cache.Delete(spanId)
}
