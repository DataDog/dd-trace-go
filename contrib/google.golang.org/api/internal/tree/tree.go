// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tree

import (
	"regexp"
	"strings"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
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
		Endpoints []*Endpoint
	}
	// An Endpoint is an API endpoint associated with a (host, method, path)
	Endpoint struct {
		Hostname     string `json:"hostname"`
		HTTPMethod   string `json:"http_method"`
		PathTemplate string `json:"path_template"`
		PathRegex    string `json:"path_regex"`
		ServiceName  string `json:"service_name"`
		ResourceName string `json:"resource_name"`

		pathMatcher *regexp.Regexp
	}
)

// New creates a new Tree. You can optionally pass endpoints to add to the
// tree.
func New(es ...*Endpoint) (*Tree, error) {
	t := &Tree{root: newTreeNode()}
	if err := t.addEndpoints(es...); err != nil {
		return nil, err
	}
	return t, nil
}

// newTreeNode creates a new treeNode.
func newTreeNode() *treeNode {
	return &treeNode{Children: map[string]*treeNode{}}
}

// addEndpoints adds zero or more endpoints to the tree.
func (t *Tree) addEndpoints(es ...*Endpoint) error {
	for _, e := range es {
		prefix := e.PathTemplate
		if idx := strings.IndexByte(prefix, '{'); idx >= 0 {
			prefix = prefix[:idx]
		}
		path := strings.SplitAfter(prefix, "/")
		if path[len(path)-1] == "" {
			path = path[:len(path)-1]
		}

		segments := append([]string{e.Hostname, e.HTTPMethod}, path...)
		t.root.add(segments, e)
	}
	return nil
}

// Get attempts to find the endpoints associated with the given hostname, http
// http method and http path. It returns false if no endpoints matched.
func (t *Tree) Get(hostname string, httpMethod string, httpPath string) (*Endpoint, bool) {
	if t == nil {
		return &Endpoint{}, false
	}
	segments := append([]string{hostname, httpMethod}, strings.SplitAfter(httpPath, "/")...)
	endpoints := t.root.getLongestPrefixMatch(segments)
	for _, e := range endpoints {
		if e.pathMatcher == nil {
			pathMatcher, err := regexp.Compile(e.PathRegex)
			if err != nil {
				log.Warn("contrib/google.golang.org/api: failed to create regex: %s: %v", e.PathRegex, err)
				continue
			}
			e.pathMatcher = pathMatcher
		}
		if e.pathMatcher.MatchString(httpPath) {
			return e, true
		}
	}
	return &Endpoint{}, false
}

// add adds an endpoint to the tree.
func (n *treeNode) add(segments []string, e *Endpoint) {
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
func (n *treeNode) getLongestPrefixMatch(segments []string) []*Endpoint {
	if len(segments) > 0 {
		child, ok := n.Children[segments[0]]
		if ok {
			es := child.getLongestPrefixMatch(segments[1:])
			if len(es) > 0 {
				return es
			}
		}
	}
	return n.Endpoints
}
