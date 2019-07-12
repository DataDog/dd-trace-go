// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2019 Datadog, Inc.

package internal

import (
	"fmt"
	"regexp"
	"strings"
)

type (
	// A Tree is a prefix tree for matching endpoints based on http requests.
	Tree struct {
		root *treeNode
	}
	// A treeNode is a node in the tree. Each node may have children based on
	// path segments:
	//
	// "" ->
	//   "v1/" ->
	//     "users/" ...
	//     "blogs/" ...
	// ...
	treeNode struct {
		Children  map[string]*treeNode
		Endpoints []Endpoint
	}
	// An Endpoint is an API endpoint associated with a (host, method, path)
	Endpoint struct {
		Hostname     string
		HTTPMethod   string
		PathTemplate string
		PathMatcher  *regexp.Regexp

		ServiceName  string
		ResourceName string
	}
)

// String returns a constructor without field names.
func (e Endpoint) String() string {
	return fmt.Sprintf(`{Hostname: "%s", HTTPMethod: "%s", PathTemplate: "%s", PathMatcher: regexp.MustCompile(`+"`"+`%s`+"`"+`), ServiceName: "%s", ResourceName: "%s"}`,
		e.Hostname, e.HTTPMethod, e.PathTemplate, e.PathMatcher.String(), e.ServiceName, e.ResourceName)
}

// NewTree creates a new Tree. You can optionally pass endpoints to add to the
// tree.
func NewTree(es ...Endpoint) *Tree {
	t := &Tree{root: newTreeNode()}
	t.Add(es...)
	return t
}

// newTreeNode creates a new treeNode.
func newTreeNode() *treeNode {
	return &treeNode{Children: map[string]*treeNode{}}
}

// Add adds zero or more endpoints to the tree.
func (t *Tree) Add(es ...Endpoint) {
	for _, e := range es {
		prefix := e.PathTemplate
		if idx := strings.IndexByte(prefix, '{'); idx >= 0 {
			prefix = prefix[:idx]
		}
		path := strings.SplitAfter(prefix, "/")
		if path[len(path)-1] == "" {
			path = path[:len(path)-1]
		}

		segments := append([]string{e.Hostname, e.HTTPMethod},
			path...)
		t.root.add(segments, e)
	}
}

// Get attempts to find the endpoints associated with the given hostname, http
// http method and http path. It returns false if no endpoints matched.
func (t *Tree) Get(hostname string, httpMethod string, httpPath string) (Endpoint, bool) {
	segments := append([]string{hostname, httpMethod},
		strings.SplitAfter(httpPath, "/")...)
	endpoints := t.root.getLongestPrefixMatch(segments)
	for _, e := range endpoints {
		if e.PathMatcher.MatchString(httpPath) {
			return e, true
		}
	}
	return Endpoint{}, false
}

// add adds an endpoint to the tree.
func (n *treeNode) add(segments []string, e Endpoint) {
	if len(segments) > 0 {
		child, ok := n.Children[segments[0]]
		if !ok {
			child = &treeNode{
				Children: map[string]*treeNode{},
			}
			n.Children[segments[0]] = child
		}
		child.add(segments[1:], e)
		return
	}
	n.Endpoints = append(n.Endpoints, e)
}

// getLongestPrefixMatch gets the endpoints for the longest prefix which match
// the segments.
//
// For example: `/api/v1/users/1234` might return `/api/v1/users/`
//
func (n *treeNode) getLongestPrefixMatch(segments []string) []Endpoint {
	if len(segments) > 0 {
		child, ok := n.Children[segments[0]]
		if ok {
			Endpoints := child.getLongestPrefixMatch(segments[1:])
			if len(Endpoints) > 0 {
				return Endpoints
			}
		}
	}
	return n.Endpoints
}
