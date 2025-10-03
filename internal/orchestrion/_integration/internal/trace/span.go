// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

package trace

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/xlab/treeprint"
)

type ID uint64

// Trace represents the root span of a trace, which is hierarchically organized
// via the Children property.
type Trace struct {
	SpanID   ID `json:"span_id"`
	ParentID ID `json:"parent_id"`
	Meta     map[string]string
	Metrics  map[string]float64
	Tags     map[string]any
	Children []*Trace
}

type Traces = []*Trace

func (tr *Trace) NumSpans() int {
	count := 1
	for _, tr := range tr.Children {
		count += tr.NumSpans()
	}
	return count
}

var _ json.Unmarshaler = &Trace{}

func (tr *Trace) UnmarshalJSON(data []byte) error {
	tr.Meta = nil
	tr.Tags = make(map[string]any)
	tr.Children = nil

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	for key, value := range raw {
		var err error
		switch key {
		case "_children":
			err = json.Unmarshal(value, &tr.Children)
		case "meta":
			err = json.Unmarshal(value, &tr.Meta)
		case "metrics":
			err = json.Unmarshal(value, &tr.Metrics)
		case "span_id":
			err = json.Unmarshal(value, &tr.SpanID)
			if err == nil {
				tr.Tags["span_id"] = json.Number(fmt.Sprintf("%d", tr.SpanID))
			}
		case "parent_id":
			err = json.Unmarshal(value, &tr.ParentID)
			if err == nil {
				tr.Tags["parent_id"] = json.Number(fmt.Sprintf("%d", tr.ParentID))
			}
		default:
			var val any
			err = json.Unmarshal(value, &val)
			tr.Tags[key] = val
		}
		if err != nil {
			return err
		}
	}

	return nil
}

func (tr *Trace) String() string {
	tree := treeprint.NewWithRoot("Root")
	tr.into(tree)
	return tree.String()
}

func (tr *Trace) into(tree treeprint.Tree) {
	keys := make([]string, 0, len(tr.Tags))
	maxLen := 1
	for key := range tr.Tags {
		keys = append(keys, key)
		if len := len(key); len > maxLen {
			maxLen = len
		}
	}
	sort.Strings(keys)
	for _, tag := range keys {
		tree.AddNode(fmt.Sprintf("%-*s = %s", maxLen, tag, printableSpanAttribute(tr.Tags, tag)))
	}

	addMapBranch(tree, tr.Meta, "meta")
	addMapBranch(tree, tr.Metrics, "metrics")

	if len(tr.Children) > 0 {
		children := tree.AddBranch("_children")
		for i, child := range tr.Children {
			child.into(children.AddBranch(fmt.Sprintf("#%d", i)))
		}
	}
}

func addMapBranch[T string | float64](tree treeprint.Tree, m map[string]T, name string) {
	if len(m) > 0 {
		keys := make([]string, 0, len(m))
		maxLen := 1
		for key := range m {
			keys = append(keys, key)
			if l := len(key); l > maxLen {
				maxLen = l
			}
		}
		sort.Strings(keys)
		br := tree.AddBranch(name)
		for _, key := range keys {
			val := m[key]
			printVal := ""
			switch v := any(val).(type) {
			case string:
				printVal = fmt.Sprintf("%q", v)
			case float64:
				printVal = strconv.FormatFloat(v, 'f', -1, 64)
			}
			br.AddNode(fmt.Sprintf("%-*s = %s", maxLen, key, printVal))
		}
	}
}

func printableSpanAttribute(attrs map[string]any, key string) string {
	val := attrs[key]

	switch t := val.(type) {
	case string:
		return fmt.Sprintf("%q", t)
	case float64:
		switch key {
		case "duration":
			d := time.Duration(int64(t)) * time.Nanosecond
			return d.String()
		case "start":
			tm := time.Unix(0, int64(t))
			return tm.Format(time.RFC3339Nano)
		default:
			return strconv.FormatFloat(t, 'f', -1, 64)
		}
	}
	return fmt.Sprintf("%+v", val)
}

// FromSimplified parses a trace tree from a simplified string format, with the following criteria:
// - 1 span per line
// - Span format is [span.name | resource.name | component | span.kind]
// - All values are optional except span.name
// - Indentation (tabs only) is used to represent parent-child relationships.
//
// Example:
//
//	[http.request | GET / | net/http | client]
//		[http.request | GET / | net/http | server]
//			[echo.someMiddleware | echo-ctx-someMiddleware]
//				[http.request | GET / | labstack/echo.v4 | server]
//					[some.span]
//					[other.span]
//	[another.root]
//		[another.root.child1]
//		[another.root.child2]
func FromSimplified(input string) Traces {
	// Trim common leading whitespace from all lines
	input = trimCommonIndentation(input)
	lines := strings.Split(input, "\n")
	result := Traces{}

	prevLevel := 0
	var curParents []*Trace
	for _, raw := range lines {
		line := strings.TrimRight(raw, " \t\r\n")
		if line == "" {
			continue
		}
		lvl := computeIndentLevel(line)
		if len(result) == 0 && lvl > 0 || lvl > prevLevel+1 {
			panic(fmt.Sprintf("invalid indentation: %q", line))
		}

		content := strings.TrimLeft(line, "\t")
		if !strings.HasPrefix(content, "[") || !strings.HasSuffix(content, "]") {
			panic(fmt.Sprintf("invalid span format: %q", content))
		}

		content = content[1 : len(content)-1]
		parts := strings.Split(content, "|")
		for i := range parts {
			parts[i] = strings.TrimSpace(parts[i])
		}

		if len(parts) < 1 || parts[0] == "" {
			panic(fmt.Sprintf("missing required span name: %q", line))
		}

		span := &Trace{
			Tags: map[string]any{
				"name": parts[0],
			},
			Meta: make(map[string]string),
		}
		if len(parts) > 1 && parts[1] != "" {
			span.Tags["resource"] = parts[1]
		}
		if len(parts) > 2 && parts[2] != "" {
			span.Meta["component"] = parts[2]
		}
		if len(parts) > 3 && parts[3] != "" {
			span.Meta["span.kind"] = parts[3]
		}

		if lvl == 0 {
			curParents = Traces{span}
			result = append(result, span)
		} else {
			// pop for every lvl decrement
			numPops := prevLevel - lvl + 1
			for i := 0; i < numPops; i++ {
				curParents = curParents[:len(curParents)-1]
			}

			parent := curParents[len(curParents)-1]
			parent.Children = append(parent.Children, span)
			curParents = append(curParents, span)
		}
		prevLevel = lvl
	}
	return result
}

// computeIndentLevel returns the indent level where 1 tab = 1 level
func computeIndentLevel(s string) int {
	level := 0
	for _, r := range s {
		switch r {
		case '\t':
			level++
		default:
			return level
		}
	}
	return level
}

// trimCommonIndentation removes the common leading whitespace from all non-empty lines.
// This allows for properly indented multi-line strings in source code.
// Only tabs are supported for indentation (following Go conventions).
func trimCommonIndentation(input string) string {
	lines := strings.Split(input, "\n")

	// Skip leading and trailing empty lines
	start := 0
	for start < len(lines) && strings.TrimSpace(lines[start]) == "" {
		start++
	}
	end := len(lines)
	for end > start && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	if start >= end {
		return ""
	}
	lines = lines[start:end]

	// Find the minimum number of leading tabs among non-empty lines
	minTabs := -1
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		tabs := 0
		for _, r := range line {
			if r == '\t' {
				tabs++
			} else {
				break
			}
		}
		if minTabs == -1 || tabs < minTabs {
			minTabs = tabs
		}
	}
	if minTabs <= 0 {
		return strings.Join(lines, "\n")
	}

	// Remove the common leading tabs
	result := make([]string, len(lines))
	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			result[i] = ""
		} else if len(line) >= minTabs {
			result[i] = line[minTabs:]
		} else {
			result[i] = line
		}
	}
	return strings.Join(result, "\n")
}

// ToSimplified returns the simplified version of a trace.
func ToSimplified(tr Traces) string {
	var sb strings.Builder
	for _, t := range tr {
		writeSimplifiedTrace(t, &sb, 0)
	}
	return sb.String()
}

func writeSimplifiedTrace(tr *Trace, sb *strings.Builder, indent int) {
	prefix := strings.Repeat("    ", indent) // 4 spaces per level

	fields := []string{fmt.Sprintf("[%s", tr.Tags["name"])}
	if resource, ok := tr.Tags["resource"]; ok {
		fields = append(fields, resource.(string))
	}
	if component, ok := tr.Meta["component"]; ok {
		fields = append(fields, component)
	}
	if kind, ok := tr.Meta["span.kind"]; ok {
		fields = append(fields, kind)
	}
	sb.WriteString(prefix + strings.Join(fields, " | ") + "]\n")

	for _, child := range tr.Children {
		writeSimplifiedTrace(child, sb, indent+1)
	}
}
