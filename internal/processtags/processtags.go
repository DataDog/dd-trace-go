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

	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

var (
	processTags         = make(map[string]string)
	processTagsTagValue = ""
	processTagsMutex    sync.RWMutex
)

func init() {
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
	if len(tags) > 0 {
		AddTags(tags)
	}
}

// AddTags merges the given tags into the global processTags map.
func AddTags(tags map[string]string) {
	processTagsMutex.Lock()
	defer processTagsMutex.Unlock()
	for k, v := range tags {
		processTags[k] = v
	}
	processTagsTagValue = serializeProcessTags(processTags)
}

// ProcessTags returns the process tags serialized to string.
func ProcessTags() string {
	processTagsMutex.RLock()
	defer processTagsMutex.RUnlock()
	return processTagsTagValue
}

func serializeProcessTags(pTags map[string]string) string {
	var b strings.Builder
	first := true
	for k, val := range pTags {
		if !first {
			b.WriteByte(',')
		}
		first = false
		b.WriteString(traceutil.NormalizeTag(k + ":" + val))
	}
	return b.String()
}
