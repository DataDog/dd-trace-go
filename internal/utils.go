// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package internal

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

// OtelTagsDelimeter is the separator between key-val pairs for OTEL env vars
const OtelTagsDelimeter = "="

// DDTagsDelimiter is the separator between key-val pairs for DD env vars
const DDTagsDelimiter = ":"

// LockMap uses an RWMutex to synchronize map access to allow for concurrent access.
// This should not be used for cases with heavy write load and performance concerns.
type LockMap struct {
	sync.RWMutex
	c uint32
	m map[string]string
}

func NewLockMap(m map[string]string) *LockMap {
	return &LockMap{m: m, c: uint32(len(m))}
}

// Iter iterates over all the map entries passing in keys and values to provided func f. Note this is READ ONLY.
func (l *LockMap) Iter(f func(key string, val string)) {
	c := atomic.LoadUint32(&l.c)
	if c == 0 { //Fast exit to avoid the cost of RLock/RUnlock for empty maps
		return
	}
	l.RLock()
	defer l.RUnlock()
	for k, v := range l.m {
		f(k, v)
	}
}

func (l *LockMap) Len() int {
	l.RLock()
	defer l.RUnlock()
	return len(l.m)
}

func (l *LockMap) Clear() {
	l.Lock()
	defer l.Unlock()
	l.m = map[string]string{}
	atomic.StoreUint32(&l.c, 0)
}

func (l *LockMap) Set(k, v string) {
	l.Lock()
	defer l.Unlock()
	if _, ok := l.m[k]; !ok {
		atomic.AddUint32(&l.c, 1)
	}
	l.m[k] = v
}

func (l *LockMap) Get(k string) string {
	l.RLock()
	defer l.RUnlock()
	return l.m[k]
}

type IntegrationTags struct {
	Rules []IntegrationTagsRule
	Cache map[string]map[string]string
}

func (i *IntegrationTags) Get(component string, instanceKeys map[string]string) map[string]string {
	instanceKeys["component"] = component
	cacheKey := mapToKey(instanceKeys)

	fmt.Println(cacheKey)
	if res, ok := i.Cache[cacheKey]; ok {
		fmt.Printf("got from cache: %s\n", cacheKey)
		return res
	}

	tags := getRuleTags(i.Rules, component, instanceKeys)
	i.Cache[cacheKey] = tags
	return tags
}

func mapToKey(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var sb strings.Builder
	for i, k := range keys {
		sb.WriteString(k)
		sb.WriteString("=")
		sb.WriteString(m[k])
		if i < len(keys)-1 {
			sb.WriteString(";")
		}
	}
	return sb.String()
}

func getRuleTags(rules []IntegrationTagsRule, component string, instanceKeys map[string]string) map[string]string {
	for _, rule := range rules {
		if rule.Component != component {
			continue
		}
		// the list of rules are joined by an OR logic
		for _, q := range rule.Query {
			// each rule's key/value pairs are joined by an AND logic (all must match)
			match := true
			for k, v := range q {
				if val, ok := instanceKeys[k]; !ok || val != v {
					// no match, check next rule
					match = false
					break
				}
			}
			if match {
				return rule.Tags
			}
		}
	}
	return nil
}

type IntegrationTagsRule struct {
	Component string              `json:"component"`
	Query     []map[string]string `json:"query"`
	Tags      map[string]string   `json:"tags"`
}
