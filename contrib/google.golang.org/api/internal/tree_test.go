// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2020 Datadog, Inc.

package internal

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTree(t *testing.T) {
	tr := NewTree([]Endpoint{
		{
			Hostname:     "www.googleapis.com",
			HTTPMethod:   "DELETE",
			PathTemplate: "/blogger/v3/blogs/{blogId}/pages/{pageId}",
			PathMatcher:  regexp.MustCompile(`^/blogger/v3/blogs/[0-9]+/pages/[0-9]+$`),
			ServiceName:  "blogger",
			ResourceName: "blogger.pages.delete",
		},
		{
			Hostname:     "www.googleapis.com",
			HTTPMethod:   "GET",
			PathTemplate: "/blogger/v3/blogs/{blogId}/pageviews",
			PathMatcher:  regexp.MustCompile(`^/blogger/v3/blogs/[0-9]+/pageviews$`),
			ServiceName:  "blogger",
			ResourceName: "blogger.pageViews.get",
		},
	}...)

	e, ok := tr.Get("www.googleapis.com", "DELETE", "/blogger/v3/blogs/1234/pages/5678")
	assert.True(t, ok)
	assert.Equal(t, "blogger", e.ServiceName)
	assert.Equal(t, "blogger.pages.delete", e.ResourceName)

	e, ok = tr.Get("www.googleapis.com", "GET", "/blogger/v3/blogs/1234/pageviews")
	assert.True(t, ok)
	assert.Equal(t, "blogger", e.ServiceName)
	assert.Equal(t, "blogger.pageViews.get", e.ResourceName)
}
