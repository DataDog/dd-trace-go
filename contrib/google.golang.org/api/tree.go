package api

import (
	"fmt"
	"regexp"
	"strings"
)

type (
	// A Tree is a prefix tree for matching endpoints based on http requests.
	Tree struct {
		root *TreeNode
	}
	// A TreeNode is a node in the tree. Each node may have children based on
	// path segments:
	//
	// "" ->
	//   "v1/" ->
	//     "users/" ...
	//     "blogs/" ...
	// ...
	TreeNode struct {
		Children  map[string]*TreeNode
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
	return fmt.Sprintf(`{"%s","%s","%s",regexp.MustCompile(`+"`"+`%s`+"`"+`),"%s","%s"}`,
		e.Hostname, e.HTTPMethod, e.PathTemplate, e.PathMatcher.String(), e.ServiceName, e.ResourceName)
}

// NewTree creates a new Tree. You can optionally pass endpoints to add to the
// tree.
func NewTree(es ...Endpoint) *Tree {
	t := &Tree{root: NewTreeNode()}
	t.Add(es...)
	return t
}

// NewTreeNode creates a new TreeNode.
func NewTreeNode() *TreeNode {
	return &TreeNode{Children: map[string]*TreeNode{}}
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
		t.root.Add(segments, e)
	}
}

// Get attempts to find the endpoints associated with the given hostname, http
// http method and http path. It returns false if no endpoints matched.
func (t *Tree) Get(hostname string, httpMethod string, httpPath string) (Endpoint, bool) {
	segments := append([]string{hostname, httpMethod},
		strings.SplitAfter(httpPath, "/")...)
	Endpoints := t.root.GetLongestPrefixMatch(segments)
	for _, e := range Endpoints {
		if e.PathMatcher.MatchString(httpPath) {
			return e, true
		}
	}
	return Endpoint{}, false
}

// Add adds an endpoint to the tree.
func (n *TreeNode) Add(segments []string, e Endpoint) {
	if len(segments) > 0 {
		child, ok := n.Children[segments[0]]
		if !ok {
			child = &TreeNode{
				Children: map[string]*TreeNode{},
			}
			n.Children[segments[0]] = child
		}
		child.Add(segments[1:], e)
		return
	}
	n.Endpoints = append(n.Endpoints, e)
}

// GetLongestPrefixMatch gets the endpoints for the longest prefix which match
// the segments.
//
// For example: `/api/v1/users/1234` might return `/api/v1/users/`
//
func (n *TreeNode) GetLongestPrefixMatch(segments []string) []Endpoint {
	if len(segments) > 0 {
		child, ok := n.Children[segments[0]]
		if ok {
			Endpoints := child.GetLongestPrefixMatch(segments[1:])
			if len(Endpoints) > 0 {
				return Endpoints
			}
		}
	}
	return n.Endpoints
}
