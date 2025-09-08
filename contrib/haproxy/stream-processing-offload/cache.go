package streamprocessingoffload

import (
	"fmt"
	"github.com/DataDog/dd-trace-go/contrib/envoyproxy/go-control-plane/v2/message_processor"
	"github.com/jellydator/ttlcache/v3"
	"github.com/negasus/haproxy-spoe-go/message"
	"time"
)

const (
	requestStateTTL = 60 * time.Second
)

var requestStateCache *ttlcache.Cache[uint64, *message_processor.RequestState]

func initRequestStateCache() {
	requestStateCache = ttlcache.New[uint64, *message_processor.RequestState](
		ttlcache.WithTTL[uint64, *message_processor.RequestState](requestStateTTL),
	)
	go requestStateCache.Start()
}

func getCurrentRequest(msg *message.Message) (message_processor.RequestState, error) {
	if requestStateCache == nil {
		return message_processor.RequestState{}, fmt.Errorf("requestStateCache is not initialized")
	}
	key := spanIDFromMessage(msg)
	if key == 0 {
		return message_processor.RequestState{}, fmt.Errorf("span_id not found in message")
	}
	if item := requestStateCache.Get(key); item != nil {
		if v := item.Value(); v != nil {
			return *v, nil
		}
	}
	return message_processor.RequestState{}, fmt.Errorf("no current request found for span_id %d", key)
}

func storeCurrentRequest(spanId uint64, rs message_processor.RequestState) error {
	if requestStateCache == nil {
		return fmt.Errorf("requestStateCache is not initialized")
	}
	local := rs
	requestStateCache.Set(spanId, &local, ttlcache.DefaultTTL)
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
