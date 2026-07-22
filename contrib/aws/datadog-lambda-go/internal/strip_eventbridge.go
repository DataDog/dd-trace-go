// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package internal

import (
	"encoding/json"
	"os"
	"strconv"
)

const (
	stripEventBridgeContextEnvVar = "DD_LAMBDA_STRIP_EVENTBRIDGE_CONTEXT"
	datadogCarrierKey             = "_datadog"
)

// StripEventBridgeContext removes detail._datadog from EventBridge Lambda events
// before they reach the user handler when DD_LAMBDA_STRIP_EVENTBRIDGE_CONTEXT=true.
//
// Extension listeners must receive the raw payload first so APM trace extraction
// is unchanged. Returns the original message when the env var is unset/false,
// the payload is not an EventBridge event, or parsing fails (fail-open).
func StripEventBridgeContext(msg json.RawMessage) json.RawMessage {
	if !stripEventBridgeContextEnabled() {
		return msg
	}
	if len(msg) == 0 {
		return msg
	}

	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(msg, &envelope); err != nil {
		return msg
	}
	if !isEventBridgeEnvelope(envelope) {
		return msg
	}

	detailRaw, ok := envelope["detail"]
	if !ok {
		return msg
	}

	cleanedDetail, changed := stripDatadogFromDetail(detailRaw)
	if !changed {
		return msg
	}

	envelope["detail"] = cleanedDetail
	out, err := json.Marshal(envelope)
	if err != nil {
		return msg
	}
	return out
}

func stripEventBridgeContextEnabled() bool {
	enabled, err := strconv.ParseBool(os.Getenv(stripEventBridgeContextEnvVar))
	// unset, invalid, or false -> do not strip (opt-in)
	return err == nil && enabled
}

func isEventBridgeEnvelope(envelope map[string]json.RawMessage) bool {
	if _, ok := envelope["detail-type"]; !ok {
		return false
	}

	sourceRaw, ok := envelope["source"]
	if !ok {
		return false
	}

	var source string
	if err := json.Unmarshal(sourceRaw, &source); err != nil {
		return false
	}

	// Match extension event_bridge_event.rs is_match: exclude scheduled events.
	return source != "aws.events"
}

func stripDatadogFromDetail(detail json.RawMessage) (json.RawMessage, bool) {
	// Case 1: detail is a JSON object (common for direct EventBridge -> Lambda).
	var detailObj map[string]json.RawMessage
	if err := json.Unmarshal(detail, &detailObj); err == nil {
		if _, ok := detailObj[datadogCarrierKey]; !ok {
			return detail, false
		}
		delete(detailObj, datadogCarrierKey)

		out, err := json.Marshal(detailObj)
		if err != nil {
			return detail, false
		}
		return out, true
	}

	// Case 2: detail is a JSON string containing an object.
	var detailStr string
	if err := json.Unmarshal(detail, &detailStr); err != nil {
		return detail, false
	}

	var nested map[string]json.RawMessage
	if err := json.Unmarshal([]byte(detailStr), &nested); err != nil {
		return detail, false
	}
	if _, ok := nested[datadogCarrierKey]; !ok {
		return detail, false
	}
	delete(nested, datadogCarrierKey)

	nestedBytes, err := json.Marshal(nested)
	if err != nil {
		return detail, false
	}

	out, err := json.Marshal(string(nestedBytes))
	if err != nil {
		return detail, false
	}
	return out, true
}