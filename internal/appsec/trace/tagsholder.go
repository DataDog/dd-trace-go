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

type serializableTag struct {
	tag any
}

func (t serializableTag) MarshalJSON() ([]byte, error) {
	return json.Marshal(t.tag)
}

// TagsHolder wraps a map holding tags. The purpose of this struct is to be
// used by composition in an Operation to allow said operation to handle tags
// addition/retrieval.
type TagsHolder struct {
	tags map[string]any
	mu   sync.RWMutex
}

// NewTagsHolder returns a new instance of a TagsHolder struct.
func NewTagsHolder() TagsHolder {
	return TagsHolder{tags: make(map[string]any)}
}

// SetTag adds the key/value pair to the tags map
func (m *TagsHolder) SetTag(k string, v any) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tags[k] = v
}

// AddSerializableTag adds the key/value pair to the tags map. Value is serialized as JSON.
func (m *TagsHolder) AddSerializableTag(k string, v any) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tags[k] = serializableTag{tag: v}
}

// Tags returns a copy of the aggregated tags map (normal and serialized)
func (m *TagsHolder) Tags() map[string]any {
	tags := make(map[string]any, len(m.tags))
	m.mu.RLock()
	defer m.mu.RUnlock()
	for k, v := range m.tags {
		tags[k] = v
		marshaler, ok := v.(serializableTag)
		if !ok {
			continue
		}
		if marshaled, err := marshaler.MarshalJSON(); err == nil {
			tags[k] = string(marshaled)
		} else {
			log.Debug("appsec: could not marshal serializable tag %s: %v", k, err)
		}
	}
	return tags
}

var _ TagSetter = (*TagsHolder)(nil) // *TagsHolder must implement TagSetter
