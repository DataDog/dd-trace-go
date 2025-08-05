package main

import (
	"log"
	"strings"

	"github.com/negasus/haproxy-spoe-go/action"
	"github.com/negasus/haproxy-spoe-go/message"
	"github.com/negasus/haproxy-spoe-go/request"
)

// Handler processes SPOE requests from HAProxy
func Handler(req *request.Request) {
	log.Printf("handle request EngineID: '%s', StreamID: '%d', FrameID: '%d' with %d messages",
		req.EngineID, req.StreamID, req.FrameID, req.Messages.Len())

	// Process each message
	for i := 0; i < req.Messages.Len(); i++ {
		msg, err := req.Messages.GetByIndex(i)
		if err != nil {
			log.Printf("Failed to get message at index %d: %v", i, err)
			continue
		}

		//log.Printf("Processing message: '%s'", msg.Name)

		switch msg.Name {
		case "http-request-headers-msg":
			handleRequestHeadersMessage(req, msg)
		case "http-request-body-msg":
			handleRequestBodyMessage(req, msg)
		case "http-response-headers-msg":
			handleResponseHeadersMessage(req, msg)
		case "http-response-body-msg":
			handleResponseBodyMessage(req, msg)
		default:
			log.Printf("Unknown message type: %s", msg.Name)
		}
	}
}

func handleRequestHeadersMessage(req *request.Request, msg *message.Message) {
	// Extract headers and analyze them
	method := getStringValue(msg, "method")
	path := getStringValue(msg, "path")
	headers := getStringValue(msg, "headers")

	log.Printf("Headers - Method: %s, Path: %s", method, path)

	isJSON := isJSONContentType(headers)

	log.Printf("Content-Type analysis - Is JSON: %t", isJSON)

	// Always mark headers as processed
	setVariable(req, "headers_processed", "true")

	req.Actions.SetVar(action.ScopeTransaction, "span_id", 1234)
}

func handleRequestBodyMessage(req *request.Request, msg *message.Message) {
	// This should only be called for JSON content
	body := getBytesArrayValue(msg, "body")

	log.Printf("Processing JSON Request body - Size: %d bytes", len(body))
}

func handleResponseHeadersMessage(req *request.Request, msg *message.Message) {
	status := getIntValue(msg, "status")
	headers := getStringValue(msg, "headers")

	log.Printf("Response Headers - Status: %d", status)
	log.Printf("Response Headers content: %s", headers)

	isJSON := isJSONContentType(headers)
	log.Printf("Response Content-Type analysis - Is JSON: %t", isJSON)
}

func handleResponseBodyMessage(req *request.Request, msg *message.Message) {
	body := getBytesArrayValue(msg, "body")

	log.Printf("Processing JSON Response body - Size: %d bytes", len(body))
}

// Helper function to set SPOE variables
func setVariable(req *request.Request, name, value string) {
	//log.Printf("Setting variable %s = %s", name, value)

	// Use the Actions interface to set variables
	if req.Actions != nil {
		// Create a set-var action using the library's action interface
		// The exact API may vary, but this is the typical pattern
		req.Actions.SetVar(action.ScopeTransaction, name, value)
	} else {
		log.Printf("WARNING: req.Actions is nil, cannot set variable %s", name)
	}
}

// Helper function to check if Content-Type indicates JSON
func isJSONContentType(headers string) bool {
	// Parse headers and look for Content-Type
	lines := strings.Split(headers, "\n")
	for _, line := range lines {
		if strings.HasPrefix(strings.ToLower(line), "content-type:") {
			contentType := strings.ToLower(strings.TrimSpace(line[13:]))
			return strings.Contains(contentType, "application/json") ||
				strings.Contains(contentType, "text/json") ||
				strings.HasSuffix(contentType, "+json")
		}
	}
	return false
}

// Helper functions to extract values from SPOE messages
func getStringValue(msg *message.Message, key string) string {
	if val, exists := msg.KV.Get(key); exists {
		if str, ok := val.(string); ok {
			return str
		}
	}
	return ""
}

func getBytesArrayValue(msg *message.Message, key string) []byte {
	if val, exists := msg.KV.Get(key); exists {
		if bytes, ok := val.([]byte); ok {
			return bytes
		}
	}
	return nil
}

func getIntValue(msg *message.Message, key string) int {
	if val, exists := msg.KV.Get(key); exists {
		switch v := val.(type) {
		case int:
			return v
		case int64:
			return int(v)
		case uint64:
			return int(v)
		}
	}
	return 0
}
