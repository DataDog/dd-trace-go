// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package llmobs

import (
	"container/list"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

const promptCacheMaxEntries = 1024

type promptCacheKey struct {
	promptID string
	selector string
}

type promptCacheEntry struct {
	key       promptCacheKey
	prompt    *ManagedPrompt
	writtenAt time.Time
}

type promptCache struct {
	mu      sync.Mutex
	entries map[promptCacheKey]*list.Element
	lru     *list.List
	ttl     time.Duration
	now     func() time.Time
}

func newPromptCache(ttl time.Duration, now func() time.Time) *promptCache {
	return &promptCache{entries: make(map[promptCacheKey]*list.Element), lru: list.New(), ttl: ttl, now: now}
}

func (c *promptCache) get(key promptCacheKey) (*ManagedPrompt, time.Time, bool, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	element := c.entries[key]
	if element == nil {
		return nil, time.Time{}, false, false
	}
	c.lru.MoveToFront(element)
	entry := element.Value.(*promptCacheEntry)
	return entry.prompt.withSource(PromptSourceCache), entry.writtenAt, c.now().Sub(entry.writtenAt) > c.ttl, true
}

func (c *promptCache) set(key promptCacheKey, prompt *ManagedPrompt, writtenAt time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if element := c.entries[key]; element != nil {
		entry := element.Value.(*promptCacheEntry)
		entry.prompt, entry.writtenAt = prompt.withSource(PromptSourceCache), writtenAt
		c.lru.MoveToFront(element)
		return
	}
	element := c.lru.PushFront(&promptCacheEntry{key: key, prompt: prompt.withSource(PromptSourceCache), writtenAt: writtenAt})
	c.entries[key] = element
	if c.lru.Len() > promptCacheMaxEntries {
		oldest := c.lru.Back()
		delete(c.entries, oldest.Value.(*promptCacheEntry).key)
		c.lru.Remove(oldest)
	}
}

func (c *promptCache) delete(key promptCacheKey) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if element := c.entries[key]; element != nil {
		delete(c.entries, key)
		c.lru.Remove(element)
	}
}

type promptFileCache struct {
	enabled bool
	dir     string
	ttl     time.Duration
	now     func() time.Time
	mu      sync.Mutex
}

type promptFileEntry struct {
	Prompt    promptDiskValue `json:"prompt"`
	WrittenAt time.Time       `json:"written_at"`
}

type promptDiskValue struct {
	ID                string         `json:"id"`
	Version           string         `json:"version"`
	Template          PromptTemplate `json:"template"`
	PromptUUID        string         `json:"prompt_uuid,omitempty"`
	PromptVersionUUID string         `json:"prompt_version_uuid,omitempty"`
}

func newPromptFileCache(enabled bool, dir string, ttl time.Duration, now func() time.Time) *promptFileCache {
	if dir == "" {
		if cacheDir, err := os.UserCacheDir(); err == nil {
			dir = filepath.Join(cacheDir, "datadog", "llmobs", "prompts")
		} else {
			dir = filepath.Join(os.TempDir(), "datadog", "llmobs", "prompts")
		}
	}
	return &promptFileCache{enabled: enabled, dir: dir, ttl: ttl, now: now}
}

func (c *promptFileCache) path(key promptCacheKey) string {
	encoded, _ := json.Marshal(struct {
		PromptID string `json:"prompt_id"`
		Selector string `json:"selector"`
	}{key.promptID, key.selector})
	hash := sha256.Sum256(encoded)
	return filepath.Join(c.dir, hex.EncodeToString(hash[:])+".json")
}

func (c *promptFileCache) get(key promptCacheKey) (*ManagedPrompt, time.Time, bool, bool) {
	if !c.enabled {
		return nil, time.Time{}, false, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	data, err := os.ReadFile(c.path(key))
	if err != nil {
		return nil, time.Time{}, false, false
	}
	var entry promptFileEntry
	if json.Unmarshal(data, &entry) != nil || entry.Prompt.ID == "" || entry.Prompt.Version == "" || entry.WrittenAt.IsZero() {
		return nil, time.Time{}, false, false
	}
	prompt, err := newManagedPrompt(entry.Prompt.ID, entry.Prompt.Version, PromptSourceCache, entry.Prompt.Template, entry.Prompt.PromptUUID, entry.Prompt.PromptVersionUUID)
	if err != nil {
		return nil, time.Time{}, false, false
	}
	return prompt, entry.WrittenAt, c.now().Sub(entry.WrittenAt) > c.ttl, true
}

func (c *promptFileCache) set(key promptCacheKey, prompt *ManagedPrompt, writtenAt time.Time) {
	if !c.enabled {
		return
	}
	entry := promptFileEntry{Prompt: promptDiskValue{ID: prompt.id, Version: prompt.version, Template: prompt.Template(), PromptUUID: prompt.promptUUID, PromptVersionUUID: prompt.promptVersionUUID}, WrittenAt: writtenAt}
	data, err := json.Marshal(entry)
	if err != nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := os.MkdirAll(c.dir, 0o700); err != nil {
		log.Debug("Failed to create prompt cache directory: %v", err.Error())
		return
	}
	temporary, err := os.CreateTemp(c.dir, ".prompt-*")
	if err != nil {
		return
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if err = temporary.Chmod(0o600); err == nil {
		_, err = temporary.Write(data)
	}
	if closeErr := temporary.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		if runtime.GOOS == "windows" {
			_ = os.Remove(c.path(key))
		}
		err = os.Rename(temporaryName, c.path(key))
	}
	if err != nil {
		log.Debug("Failed to write prompt cache: %v", err.Error())
	}
}

func (c *promptFileCache) delete(key promptCacheKey) {
	if !c.enabled {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	_ = os.Remove(c.path(key))
}
