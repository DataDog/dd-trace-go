package haproxy

import (
	"encoding/json"
	"fmt"
	"github.com/negasus/haproxy-spoe-go/action"
	"log"
	"strings"

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

		log.Printf("Processing message: '%s'", msg.Name)

		switch msg.Name {
		case "http-headers-msg":
			handleHeadersMessage(req, msg)
		case "http-body-conditional-msg":
			handleBodyMessage(req, msg)
		case "http-response-headers-msg":
			handleResponseHeadersMessage(req, msg)
		case "http-response-body-conditional-msg":
			handleResponseBodyMessage(req, msg)
		default:
			log.Printf("Unknown message type: %s", msg.Name)
		}
	}
}

func handleHeadersMessage(req *request.Request, msg *message.Message) {
	// Extract headers and analyze them
	method := getStringValue(msg, "method")
	path := getStringValue(msg, "path")
	headers := getStringValue(msg, "headers")
	bodySize := getIntValue(msg, "body_size")

	log.Printf("Headers - Method: %s, Path: %s, Body Size: %d", method, path, bodySize)

	// Check if Content-Type indicates JSON
	isJSON := isJSONContentType(headers)
	shouldProcessBody := isJSON && bodySize > 0

	log.Printf("Content-Type analysis - Is JSON: %t, Should process body: %t", isJSON, shouldProcessBody)

	// Set control variables for HAProxy using simple string actions
	if shouldProcessBody {
		// Enable body processing for JSON content
		setVariable(req, "process_body", "true")
		log.Printf("Enabled body processing for JSON content")
	} else {
		// Disable body processing for non-JSON content
		setVariable(req, "skip_body", "true")
		log.Printf("Disabled body processing - not JSON or no body")
	}

	// Always mark headers as processed
	setVariable(req, "headers_processed", "true")
}

func handleBodyMessage(req *request.Request, msg *message.Message) {
	// This should only be called for JSON content
	body := getStringValue(msg, "body")

	log.Printf("Processing JSON body - Size: %d bytes", len(body))

	// Validate JSON
	var jsonObj interface{}
	if err := json.Unmarshal([]byte(body), &jsonObj); err != nil {
		log.Printf("Invalid JSON body: %v", err)

		// Set error flag
		setVariable(req, "agent.json_error", "true")
		return
	}

	log.Printf("Valid JSON body processed successfully")

	// Example: Extract specific fields from JSON
	if jsonMap, ok := jsonObj.(map[string]interface{}); ok {
		if userID, exists := jsonMap["user_id"]; exists {
			log.Printf("Found user_id in JSON: %v", userID)
			setVariable(req, "agent.user_id", fmt.Sprintf("%v", userID))
		}

		if actionVal, exists := jsonMap["action"]; exists {
			log.Printf("Found action in JSON: %v", actionVal)
			setVariable(req, "agent.json_action", fmt.Sprintf("%v", actionVal))
		}

		// Example: Check for suspicious patterns
		if checkSuspiciousContent(jsonMap) {
			log.Printf("Suspicious content detected in JSON")
			setVariable(req, "agent.suspicious", "true")
		}
	}

	// Mark body as successfully processed
	setVariable(req, "agent.body_processed", "true")
}

func handleResponseHeadersMessage(req *request.Request, msg *message.Message) {
	status := getIntValue(msg, "status")
	headers := getStringValue(msg, "headers")
	bodySize := getIntValue(msg, "body_size")

	log.Printf("Response Headers - Status: %d, Body Size: %d", status, bodySize)
	log.Printf("Response Headers content: %s", headers)

	// Only process response body for successful JSON responses
	isJSON := isJSONContentType(headers)
	shouldProcessResponseBody := isJSON && bodySize > 0 && status >= 200 && status < 300

	log.Printf("Response Content-Type analysis - Is JSON: %t, Should process body: %t", isJSON, shouldProcessResponseBody)

	if shouldProcessResponseBody {
		// Use the CORRECT variable name that matches spoe.conf
		setVariable(req, "agent.process_response_body", "true")
		log.Printf("Enabled response body processing for JSON")
	} else {
		log.Printf("Disabled response body processing - not JSON, no body, or error status")
	}
}

func handleResponseBodyMessage(req *request.Request, msg *message.Message) {
	body := getStringValue(msg, "body")

	log.Printf("Processing JSON response body - Size: %d bytes", len(body))

	// Validate response JSON
	var jsonObj interface{}
	if err := json.Unmarshal([]byte(body), &jsonObj); err != nil {
		log.Printf("Invalid JSON response body: %v", err)
		return
	}

	log.Printf("Valid JSON response body processed")
}

// Helper function to set SPOE variables
func setVariable(req *request.Request, name, value string) {
	log.Printf("Setting variable %s = %s", name, value)

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

// Example function to check for suspicious content
func checkSuspiciousContent(jsonMap map[string]interface{}) bool {
	// Example: Check for common attack patterns
	suspiciousKeys := []string{"<script", "javascript:", "eval(", "document.cookie"}

	for key, value := range jsonMap {
		valueStr := fmt.Sprintf("%v", value)
		keyStr := fmt.Sprintf("%v", key)

		for _, suspicious := range suspiciousKeys {
			if strings.Contains(strings.ToLower(valueStr), suspicious) ||
				strings.Contains(strings.ToLower(keyStr), suspicious) {
				return true
			}
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
