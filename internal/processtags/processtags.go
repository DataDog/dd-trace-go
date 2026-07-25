// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package processtags

import (
	"maps"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/DataDog/datadog-agent/pkg/trace/traceutil/normalize"

	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

const (
	tagEntrypointName    = "entrypoint.name"
	tagEntrypointBasedir = "entrypoint.basedir"
	tagEntrypointWorkdir = "entrypoint.workdir"
	tagEntrypointType    = "entrypoint.type"
	tagSvcUser           = "svc.user"
	tagSvcAuto           = "svc.auto"
)

const (
	entrypointTypeExecutable = "executable"
)

var (
	processStateMu            sync.RWMutex
	reloadMu                  sync.Mutex
	enabled                   bool
	enabledProvider           = func() bool { return true }
	collector                 = collect
	initialized               bool
	pTags                     *ProcessTags
	containerTagsHashRegistry = newContainerTagsHashRegistry()
)

type containerHashRegistry struct {
	mu sync.Mutex

	publicationID uint64
	generation    uint64
	revision      uint64
	value         atomic.Value
}

func newContainerTagsHashRegistry() *containerHashRegistry {
	return new(containerHashRegistry)
}

func (r *containerHashRegistry) apply(publicationID, generation, revision uint64, hash string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if publicationID < r.publicationID ||
		(publicationID == r.publicationID && generation < r.generation) ||
		(publicationID == r.publicationID && generation == r.generation && revision <= r.revision) {
		return false
	}
	r.publicationID = publicationID
	r.generation = generation
	r.revision = revision
	r.value.Store(hash)
	return true
}

type ProcessTags struct {
	mu sync.RWMutex
	// +checklocks:mu
	tags map[string]string

	sliceAtomic atomic.Pointer[[]string]
	strAtomic   atomic.Pointer[string]
}

// String returns the string representation of the process tags.
func (p *ProcessTags) String() string {
	if p == nil {
		return ""
	}
	if s := p.strAtomic.Load(); s != nil {
		return *s
	}
	return ""
}

// Slice returns the string slice representation of the process tags.
func (p *ProcessTags) Slice() []string {
	if p == nil {
		return nil
	}
	if s := p.sliceAtomic.Load(); s != nil {
		return *s
	}
	return nil
}

func (p *ProcessTags) merge(newTags map[string]string) {
	if len(newTags) == 0 {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.tags == nil {
		p.tags = make(map[string]string)
	}
	maps.Copy(p.tags, newTags)
	p.rebuild()
}

// rebuild re-serializes p.tags into p.str and p.slice.
// Must be called with p.mu held for writing.
// +checklocks:p.mu
func (p *ProcessTags) rebuild() {
	// loop over the sorted map keys so the resulting string and slice versions are created consistently.
	keys := make([]string, 0, len(p.tags))
	for k := range p.tags {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	tagsSlice := make([]string, 0, len(p.tags))
	var b strings.Builder
	first := true
	for _, k := range keys {
		val := p.tags[k]
		if !first {
			b.WriteByte(',')
		}
		first = false
		keyVal := normalize.NormalizeTag(k + ":" + val)
		b.WriteString(keyVal)
		tagsSlice = append(tagsSlice, keyVal)
	}
	str := b.String()
	p.sliceAtomic.Store(&tagsSlice)
	p.strAtomic.Store(&str)
}

// Reload initializes the configuration and process tags collection. This is useful for tests.
func Reload() {
	processStateMu.RLock()
	provider := enabledProvider
	processStateMu.RUnlock()
	ReloadWithEnabled(provider())
}

// SetEnabledProvider sets the resolver sampled by the next Reload. The config
// package installs the process-wide resolver during package initialization.
// Concurrent updates are safe; an in-progress Reload uses the resolver snapshot
// it loaded before invoking the callback.
func SetEnabledProvider(provider func() bool) {
	processStateMu.Lock()
	defer processStateMu.Unlock()
	if provider == nil {
		enabledProvider = func() bool { return true }
		return
	}
	enabledProvider = provider
}

// ReloadWithEnabled initializes process tags from an already-resolved gate.
func ReloadWithEnabled(isEnabled bool) {
	reloadMu.Lock()
	defer reloadMu.Unlock()
	reloadWithEnabledLocked(isEnabled)
}

func reloadWithEnabledLocked(isEnabled bool) {
	if !isEnabled {
		processStateMu.Lock()
		enabled = false
		pTags = nil
		initialized = true
		processStateMu.Unlock()
		return
	}

	processStateMu.RLock()
	collectTags := collector
	processStateMu.RUnlock()
	tags := collectTags()
	next := new(ProcessTags)
	if len(tags) > 0 {
		next.merge(tags)
	}
	processStateMu.Lock()
	pTags = next
	enabled = true
	initialized = true
	processStateMu.Unlock()
}

func ensureInitialized() {
	processStateMu.RLock()
	isInitialized := initialized
	provider := enabledProvider
	processStateMu.RUnlock()
	if isInitialized {
		return
	}
	isEnabled := provider()

	reloadMu.Lock()
	defer reloadMu.Unlock()
	processStateMu.RLock()
	isInitialized = initialized
	processStateMu.RUnlock()
	if !isInitialized {
		reloadWithEnabledLocked(isEnabled)
	}
}

func collect() map[string]string {
	tags := make(map[string]string)
	execPath, err := os.Executable()
	if err != nil {
		log.Debug("failed to get binary path: %s", err.Error())
	} else {
		tags[tagEntrypointName] = filepath.Base(execPath)
		if baseDirName, ok := directoryTagValue(filepath.Dir(execPath)); ok {
			tags[tagEntrypointBasedir] = baseDirName
		}
		tags[tagEntrypointType] = entrypointTypeExecutable
	}
	wd, err := os.Getwd()
	if err != nil {
		log.Debug("failed to get working directory: %s", err.Error())
	} else {
		if workDirName, ok := directoryTagValue(wd); ok {
			tags[tagEntrypointWorkdir] = workDirName
		}
	}
	return tags
}

func directoryTagValue(dir string) (string, bool) {
	if dir == "" {
		return "", false
	}
	name := filepath.Base(dir)
	if name == "" || name == "bin" || name == string(os.PathSeparator) {
		return "", false
	}
	return name, true
}

// GlobalTags returns the global process tags.
func GlobalTags() *ProcessTags {
	ensureInitialized()
	processStateMu.RLock()
	defer processStateMu.RUnlock()
	if !enabled {
		return nil
	}
	return pTags
}

// TagsWithServiceName returns a process-tag snapshot with the supplied service
// marker applied, without mutating the active process tags.
func TagsWithServiceName(name string, isUserDefined bool) []string {
	tagsSnapshot := GlobalTags()
	if tagsSnapshot == nil {
		return nil
	}
	tagsSnapshot.mu.RLock()
	tags := maps.Clone(tagsSnapshot.tags)
	tagsSnapshot.mu.RUnlock()
	if tags == nil {
		tags = make(map[string]string)
	}
	delete(tags, tagSvcAuto)
	delete(tags, tagSvcUser)
	if isUserDefined {
		tags[tagSvcUser] = "true"
	} else {
		tags[tagSvcAuto] = name
	}
	keys := make([]string, 0, len(tags))
	for key := range tags {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, normalize.NormalizeTag(key+":"+tags[key]))
	}
	return result
}

// SetContainerTagsHash stores the container tags hash returned by the agent.
func SetContainerTagsHash(hash string) {
	containerTagsHashRegistry.value.Store(hash)
}

// ContainerTagsHash returns the latest container tags hash returned by the agent.
func ContainerTagsHash() string {
	hash, _ := containerTagsHashRegistry.value.Load().(string)
	return hash
}

// ApplyContainerTagsHashForPublication stores hash when the supplied
// publication tuple is newer than the last applied tuple.
func ApplyContainerTagsHashForPublication(publicationID, generation, revision uint64, hash string) bool {
	return containerTagsHashRegistry.apply(publicationID, generation, revision, hash)
}

// SetServiceNameTag sets the appropriate process tag for the global service name.
// svc.user and svc.auto are mutually exclusive: calling this function removes the
// previously set tag before adding the new one.
// If isUserDefined is true, sets svc.user:true; otherwise sets svc.auto:<name>.
func SetServiceNameTag(name string, isUserDefined bool) {
	tags := GlobalTags()
	if tags == nil {
		return
	}
	tags.mu.Lock()
	defer tags.mu.Unlock()
	if tags.tags == nil {
		tags.tags = make(map[string]string)
	}
	delete(tags.tags, tagSvcAuto)
	delete(tags.tags, tagSvcUser)
	if isUserDefined {
		tags.tags[tagSvcUser] = "true"
	} else {
		tags.tags[tagSvcAuto] = name
	}
	tags.rebuild()
}

func setCollectorForTesting(provider func() map[string]string) func() {
	processStateMu.Lock()
	previous := collector
	collector = provider
	processStateMu.Unlock()
	return func() {
		processStateMu.Lock()
		collector = previous
		processStateMu.Unlock()
	}
}

func resetInitializationForTesting() {
	reloadMu.Lock()
	defer reloadMu.Unlock()
	processStateMu.Lock()
	enabled = false
	initialized = false
	pTags = nil
	processStateMu.Unlock()
}

func setProcessTagsForTesting(tags *ProcessTags) {
	reloadMu.Lock()
	defer reloadMu.Unlock()
	processStateMu.Lock()
	pTags = tags
	enabled = tags != nil
	initialized = true
	processStateMu.Unlock()
}
