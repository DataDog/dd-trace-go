// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package trace

import (
	"encoding/json"
	"sync"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

// TagsHolder wraps a map holding tags. The purpose of this struct is to be
// used by composition in an Operation to allow said operation to handle tags
// addition/retrieval.
type TagsHolder struct {
	tags             map[string]any
	tagsMu           sync.RWMutex
	serializableTags map[string]any
	sTagsMu          sync.RWMutex
}

// NewTagsHolder returns a new instance of a TagsHolder struct.
func NewTagsHolder() TagsHolder {
	return TagsHolder{tags: make(map[string]any), serializableTags: make(map[string]any)}
}

// AddTag adds the key/value pair to the tags map
func (m *TagsHolder) AddTag(k string, v any) {
	m.tagsMu.Lock()
	defer m.tagsMu.Unlock()
	m.tags[k] = v
}

// AddSerializableTag adds the key/value pair to the tags map. Value is serialized
func (m *TagsHolder) AddSerializableTag(k string, v any) {
	m.sTagsMu.Lock()
	defer m.sTagsMu.Unlock()
	m.serializableTags[k] = v
}

// Tags returns a copy of the aggregated tags map (normal and serialized)
func (m *TagsHolder) Tags() map[string]any {
	tags := make(map[string]any, len(m.tags)+len(m.serializableTags))
	m.sTagsMu.RLock()
	for k, v := range m.serializableTags {
		if value, err := json.Marshal(v); err == nil {
			tags[k] = string(value)
		} else {
			log.Debug("appsec: could not marshal serializable tag '%s': %v", k, err)
		}
	}
	m.sTagsMu.RUnlock() // Don't defer this unlock as we can release the mutex early
	m.tagsMu.RLock()
	defer m.tagsMu.RUnlock()
	for k, v := range m.tags {
		tags[k] = v
	}
	return tags
}
