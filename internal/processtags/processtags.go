// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package processtags

import (
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/DataDog/datadog-agent/pkg/trace/traceutil"

	"github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

var (
	enabled bool
	pTags   *ProcessTags
)

func init() {
	ResetConfig()
}

type ProcessTags struct {
	mu    sync.RWMutex
	tags  map[string]string
	str   string
	slice []string
}

// String returns the string representation of the process tags.
func (p *ProcessTags) String() string {
	if p == nil {
		return ""
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.str
}

// Slice returns the string slice representation of the process tags.
func (p *ProcessTags) Slice() []string {
	if p == nil {
		return nil
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.slice
}

func (p *ProcessTags) mergeTags(newTags map[string]string) {
	if len(newTags) == 0 {
		return
	}
	pTags.mu.Lock()
	defer pTags.mu.Unlock()

	if p.tags == nil {
		p.tags = make(map[string]string)
	}
	for k, v := range newTags {
		p.tags[k] = v
	}

	tagsSlice := make([]string, 0, len(p.tags))
	var b strings.Builder
	first := true
	for k, val := range p.tags {
		if !first {
			b.WriteByte(',')
		}
		first = false
		keyVal := traceutil.NormalizeTag(k + ":" + val)
		b.WriteString(keyVal)
		tagsSlice = append(tagsSlice, keyVal)
	}
	p.slice = tagsSlice
	p.str = b.String()
}

// ResetConfig initializes the configuration and process tags collection. This is useful for tests.
func ResetConfig() {
	enabled = internal.BoolEnv("DD_EXPERIMENTAL_COLLECT_PROCESS_TAGS_ENABLED", false)
	if !enabled {
		return
	}
	pTags = &ProcessTags{}
	tags := collectInitialProcessTags()
	if len(tags) > 0 {
		AddTags(tags)
	}
}

func collectInitialProcessTags() map[string]string {
	tags := make(map[string]string)
	execPath, err := os.Executable()
	if err != nil {
		log.Debug("failed to get binary path: %v", err)
	} else {
		baseDirName := filepath.Base(filepath.Dir(execPath))
		tags["entrypoint.name"] = filepath.Base(execPath)
		tags["entrypoint.basedir"] = baseDirName
	}
	wd, err := os.Getwd()
	if err != nil {
		log.Debug("failed to get working directory: %v", err)
	} else {
		tags["workdir"] = filepath.Base(wd)
	}
	return tags
}

// Get returns the global process tags.
func Get() *ProcessTags {
	if !enabled {
		return nil
	}
	return pTags
}

// AddTags merges the given tags into the global processTags map.
func AddTags(tags map[string]string) {
	if !enabled {
		return
	}
	pTags.mergeTags(tags)
}
