package streamprocessingoffload

import (
	"github.com/DataDog/dd-trace-go/contrib/envoyproxy/go-control-plane/v2/message_processor"
	"github.com/negasus/haproxy-spoe-go/action"
	"github.com/negasus/haproxy-spoe-go/message"
	"github.com/negasus/haproxy-spoe-go/request"
	"log"
)

// Helper functions to extract values from SPOE messages
func getStringValue(msg *message.Message, key string) string {
	if val, exists := msg.KV.Get(key); exists {
		if str, ok := val.(string); ok {
			return str
		}
	}
	return ""
}
func getIntValue(msg *message.Message, key string) int {
	if val, exists := msg.KV.Get(key); exists {
		if i, ok := val.(int); ok {
			return i
		}
	}
	return 0
}

func getBytesArrayValue(msg *message.Message, key string) []byte {
	if val, exists := msg.KV.Get(key); exists {
		if bytes, ok := val.([]byte); ok {
			return bytes
		}
	}
	return nil
}

func getBoolValue(msg *message.Message, key string) bool {
	if val, exists := msg.KV.Get(key); exists {
		if b, ok := val.(bool); ok {
			return b
		}
	}
	return false
}

func spanIDFromMessage(msg *message.Message) uint64 {
	if val, exists := msg.KV.Get("span_id"); exists {
		switch v := val.(type) {
		case uint64:
			return v
		}
	}
	return 0
}

func setBlockResponseData(data *message_processor.BlockResponseData, req *request.Request) {
	if req.Actions == nil {
		log.Printf("WARNING: req.Actions is nil, cannot set block response data")
		return
	}

	req.Actions.SetVar(action.ScopeTransaction, "blocked", true)
	req.Actions.SetVar(action.ScopeTransaction, "body", data.Body)
	req.Actions.SetVar(action.ScopeTransaction, "status_code", data.StatusCode)
	req.Actions.SetVar(action.ScopeTransaction, "content_type", data.Headers.Get("content-type"))
}
