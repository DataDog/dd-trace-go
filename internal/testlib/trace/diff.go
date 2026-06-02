// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

package trace

import (
	"fmt"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/xlab/treeprint"
)

type Diff treeprint.Tree

const (
	markerAdded   = "+"
	markerRemoved = "-"
	markerChanged = "±"
	markerEqual   = "="
)

// RequireAnyMatch asserts that any of the traces in `got` corresponds to the receiver.
func (tr *Trace) RequireAnyMatch(t testing.TB, got []*Trace) {
	t.Helper()

	foundTrace, diff := tr.matchesAny(got, treeprint.NewWithRoot("Root"))
	if foundTrace == nil {
		assert.Fail(t, "no match found for trace", diff)
	} else {
		t.Logf("Found matching trace:\n%s", foundTrace)
	}
}

func (tr *Trace) matchesAny(others []*Trace, diff treeprint.Tree) (*Trace, Diff) {
	if len(others) == 0 {
		tr.into(diff.AddMetaBranch(markerRemoved, "No spans to match against"))
		return nil, diff
	}

	for idx, other := range others {
		id := fmt.Sprintf("Trace at index %d", idx)
		if other.SpanID != 0 {
			id = fmt.Sprintf("Span ID %d", other.SpanID)
		}
		branch := diff.AddMetaBranch(markerChanged, id)
		if tr.matches(other, branch) {
			return other, nil
		}
	}
	return nil, diff
}

// macthes determines whether the receiving span matches the other span, and
// adds difference information to the provided diff tree.
func (tr *Trace) matches(other *Trace, diff treeprint.Tree) (matches bool) {
	matches = true

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
		expected := tr.Tags[tag]
		actual := other.Tags[tag]
		if expected != actual && (tag != "service" || fmt.Sprintf("%s.exe", expected) != actual) {
			branch := diff.AddMetaBranch(markerChanged, tag)
			branch.AddMetaNode(markerRemoved, expected)
			branch.AddMetaNode(markerAdded, actual)
			matches = false
		} else {
			diff.AddMetaNode(markerEqual, fmt.Sprintf("%-*s = %q", maxLen, tag, expected))
		}
	}

	keys = make([]string, 0, len(tr.Meta))
	maxLen = 1
	for key := range tr.Meta {
		keys = append(keys, key)
		if len := len(key); len > maxLen {
			maxLen = len
		}
	}
	sort.Strings(keys)
	var metaNode treeprint.Tree
	for _, key := range keys {
		expected := tr.Meta[key]
		actual, actualExists := other.Meta[key]
		if metaNode == nil {
			metaNode = diff.AddBranch("meta")
		}
		if expected != actual {
			branch := metaNode.AddMetaBranch(markerChanged, key)
			branch.AddMetaNode(markerRemoved, expected)
			if actualExists {
				branch.AddMetaNode(markerAdded, actual)
			} else {
				branch.AddMetaNode(markerAdded, nil)
			}
			matches = false
		} else {
			metaNode.AddMetaNode(markerEqual, fmt.Sprintf("%-*s = %q", maxLen, key, expected))
		}
	}

	var childrenNode treeprint.Tree
	for idx, child := range tr.Children {
		if childrenNode == nil {
			childrenNode = diff.AddBranch("_children")
		}
		nodeName := fmt.Sprintf("At index %d", idx)
		if len(other.Children) == 0 {
			child.into(childrenNode.AddMetaBranch(markerRemoved, fmt.Sprintf("%s (no children to match from)", nodeName)))
			matches = false
			continue
		}

		if span, childDiff := child.matchesAny(other.Children, treeprint.New()); span != nil {
			if span.SpanID != 0 {
				nodeName = fmt.Sprintf("Span #%d", span.SpanID)
			}
			child.into(childrenNode.AddMetaBranch(markerEqual, nodeName))
		} else {
			childDiff.SetMetaValue(markerChanged)
			childDiff.SetValue(nodeName)
			childrenNode.AddNode(childDiff)
			matches = false
		}
	}

	return
}
