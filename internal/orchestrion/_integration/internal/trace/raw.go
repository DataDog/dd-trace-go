// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

package trace

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

// RawSpan represents a raw span from the fake agent. These spans are not linked
// together in a trace hierarchically, instead they have an `ID` and `ParentID`
// field that can be used to reconstruct the hierarchy.
type RawSpan struct {
	ParentID SpanID `json:"parent_id"`
	Trace
}

var _ json.Unmarshaler = &RawSpan{}

func ParseRaw(data []byte, traces *[]*Trace) error {
	var rawSpanGroups [][]*RawSpan
	if err := json.Unmarshal(data, &rawSpanGroups); err != nil {
		return err
	}

	// First pass: make the spans ID-addressable
	spans := make(map[SpanID]*RawSpan)
	for _, rawSpans := range rawSpanGroups {
		for _, span := range rawSpans {
			if span.ID == 0 {
				return errors.New("invalid span (span_id is 0)")
			}
			spans[span.ID] = span
		}
	}

	// Second pass: build up parent-child relationships
	roots := make([]*Trace, 0, len(spans))
	for _, span := range spans {
		if span.ParentID == 0 {
			// This is a root span
			roots = append(roots, &span.Trace)
			continue
		}

		// This is a child span
		parent, found := spans[span.ParentID]
		if !found {
			return fmt.Errorf("span %d has unknown parent %d", span.ID, span.ParentID)
		}
		parent.Children = append(parent.Children, &span.Trace)
	}

	// We're done here!
	*traces = roots
	return nil
}

func (span *RawSpan) UnmarshalJSON(data []byte) error {
	span.ID = 0
	span.ParentID = 0
	span.Trace = Trace{Tags: make(map[string]any)}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil
	}

	for key, value := range raw {
		var err error
		switch key {
		case "span_id":
			err = json.Unmarshal(value, &span.ID)
			if err == nil {
				span.Tags["span_id"] = json.Number(fmt.Sprintf("%d", span.ID))
			}
		case "parent_id":
			err = json.Unmarshal(value, &span.ParentID)
			if err == nil {
				span.Tags["parent_id"] = json.Number(fmt.Sprintf("%d", span.ParentID))
			}
		case "_children":
			err = json.Unmarshal(value, &span.Children)
		case "meta":
			err = json.Unmarshal(value, &span.Meta)
		case "metrics":
			err = json.Unmarshal(value, &span.Metrics)
		default:
			var val any
			dec := json.NewDecoder(bytes.NewReader(value))
			dec.UseNumber()
			err = dec.Decode(&val)
			span.Tags[key] = val
		}
		if err != nil {
			return err
		}
	}

	return nil
}
