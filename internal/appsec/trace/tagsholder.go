// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package trace

import "sync"

// TagsHolder wraps a map holding tags. The purpose of this struct is to be
// used by composition in an Operation to allow said operation to handle tags
// addition/retrieval.
type TagsHolder struct {
	tags map[string]interface{}
	mu   sync.Mutex
}

// NewTagsHolder returns a new instance of a TagsHolder struct.
func NewTagsHolder() TagsHolder {
	return TagsHolder{tags: map[string]interface{}{}}
}

// AddTag adds the key/value pair to the tags map
func (m *TagsHolder) AddTag(k string, v interface{}) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tags[k] = v
}

// Tags returns the tags map
func (m *TagsHolder) Tags() map[string]interface{} {
	return m.tags
}
