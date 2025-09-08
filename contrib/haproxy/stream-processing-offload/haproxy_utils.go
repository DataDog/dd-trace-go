package streamprocessingoffload

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/DataDog/dd-trace-go/contrib/envoyproxy/go-control-plane/v2/message_processor"
	"github.com/negasus/haproxy-spoe-go/action"
	"github.com/negasus/haproxy-spoe-go/message"
	"github.com/negasus/haproxy-spoe-go/request"
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

// spanIDFromMessage extracts the span_id from the agent message to use as the key for the request state cache.
func spanIDFromMessage(msg *message.Message) (uint64, error) {
	spanIdStr := getStringValue(msg, "span_id")

	if spanIdStr == "" {
		return 0, fmt.Errorf("span_id not found in message")
	}

	spanId, err := strconv.ParseUint(spanIdStr, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("failed to parse span_id '%s': %v", spanIdStr, err)
	}
	if spanId == 0 {
		return 0, fmt.Errorf("span_id is 0")
	}

	return spanId, nil
}

// setHeadersResponseData sets HeadersResponseData data into the request variables answering a Request Headers message
func setHeadersResponseData(data *message_processor.HeadersResponseData, req *request.Request, reqState *message_processor.RequestState) error {
	if req.Actions == nil {
		return fmt.Errorf("req.Actions is nil, cannot set headers response data")
	}

	// Only set the span id from a request headers message
	if data.Direction == message_processor.DirectionRequest {
		spanId := reqState.Span.Context().SpanID()

		err := storeCurrentRequest(spanId, *reqState)
		if err != nil {
			return err
		}

		spanIdStr := strconv.FormatUint(spanId, 10)
		req.Actions.SetVar(action.ScopeTransaction, "span_id", spanIdStr)
	}

	if data.RequestBody {
		req.Actions.SetVar(action.ScopeTransaction, "request_body", true)
	}

	return nil
}

// setBlockResponseData sets blocked data into the request variables when the request is blocked
func setBlockResponseData(data *message_processor.BlockResponseData, req *request.Request) error {
	if req.Actions == nil {
		return fmt.Errorf("req.Actions is nil, cannot set block response data")
	}

	req.Actions.SetVar(action.ScopeTransaction, "blocked", true)
	req.Actions.SetVar(action.ScopeTransaction, "headers", convertHeadersToString(data.Headers))
	req.Actions.SetVar(action.ScopeTransaction, "body", data.Body)
	req.Actions.SetVar(action.ScopeTransaction, "status_code", data.StatusCode)

	return nil
}

// convertHeadersToString converts HTTP headers to a string format with Header: Value pairs separated by newlines.
// These headers will then be parsed by a lua script in the haproxy module.
func convertHeadersToString(headers http.Header) string {
	var sb strings.Builder
	for key, values := range headers {
		sb.WriteString(key)
		sb.WriteString(": ")
		sb.WriteString(strings.Join(values, ","))
		sb.WriteString("\n")
	}
	return sb.String()
}
