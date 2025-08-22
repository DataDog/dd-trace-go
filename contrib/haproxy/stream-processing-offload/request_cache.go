package streamprocessingoffload

import (
	"context"
	"fmt"
	"github.com/DataDog/dd-trace-go/contrib/envoyproxy/go-control-plane/v2/message_processor"
	"github.com/jellydator/ttlcache/v3"
	"github.com/negasus/haproxy-spoe-go/message"
	"time"
)

var requestStateCache *ttlcache.Cache[uint64, *message_processor.RequestState]

func initRequestStateCache() {
	const requestStateTTL = time.Minute // Default TTL but will be overridden by the timeout value of the HAProxy configuration
	requestStateCache = ttlcache.New[uint64, *message_processor.RequestState](
		ttlcache.WithTTL[uint64, *message_processor.RequestState](requestStateTTL),
	)

	// TODO: inject cleanup method
	requestStateCache.OnEviction(func(ctx context.Context, reason ttlcache.EvictionReason, item *ttlcache.Item[uint64, *message_processor.RequestState]) {
		fmt.Println(item.Key(), item.Value())
		item.Value().Close()
	})

	go requestStateCache.Start()
}

func getCurrentRequest(msg *message.Message) (*message_processor.RequestState, error) {
	if requestStateCache == nil {
		return nil, fmt.Errorf("requestStateCache is not initialized")
	}
	key, err := spanIDFromMessage(msg)
	if err != nil {
		return nil, fmt.Errorf("failed to extract span_id from message: %w", err)
	}

	if item := requestStateCache.Get(key); item != nil {
		if v := item.Value(); v != nil {
			return v, nil
		}
	}

	return nil, fmt.Errorf("no current request found for span_id %d", key)
}

func storeCurrentRequest(spanId uint64, rs message_processor.RequestState, timeout string) error {
	if requestStateCache == nil {
		return fmt.Errorf("requestStateCache is not initialized")
	}

	timeoutValue, err := time.ParseDuration(timeout)
	if err != nil {
		instr.Logger().Warn("haproxy_spoa: the timeout value '%s' is invalid. Please configure correctly the DD_SPOA_TIMEOUT variable in your HAProxy global configuration. Fallback to 1 minute.", timeout)
		timeoutValue = time.Minute // Fallback to a TTL of 1 minute
	}

	local := rs
	requestStateCache.Set(spanId, &local, timeoutValue)
	return nil
}

// deleteCurrentRequest removes a RequestState from the cache; call this at end of request lifecycle
func deleteCurrentRequest(spanId uint64) error {
	if requestStateCache == nil {
		return fmt.Errorf("requestStateCache is not initialized")
	}
	requestStateCache.Delete(spanId)
	return nil
}
