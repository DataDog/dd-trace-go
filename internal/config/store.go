// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package config

import (
	"crypto/sha256"
	"errors"
	"maps"
	"math"
	"net/url"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/DataDog/dd-trace-go/v2/internal/globalconfig"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/processtags"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
)

var (
	errInvalidPreparedClaims = errors.New("config: prepared claims do not match the staged generation")
	errClaimRevisionConflict = errors.New("config: product claim changed before tracer publication")
	errPublicationBusy       = errors.New("config: another generation handoff is in progress")
)

var shareableClaimNames = map[string]struct{}{
	"DD_SERVICE":         {},
	"DD_ENV":             {},
	"DD_VERSION":         {},
	"DD_SITE":            {},
	"DD_TRACE_AGENT_URL": {},
	"DD_TAGS":            {},
}

type unsupportedClaimValue struct{}

type canonicalTagClaim struct {
	Entries []canonicalTagEntry
}

type canonicalTagEntry struct {
	Key   string
	Value string
}

type urlClaimFingerprint struct {
	Digest [sha256.Size]byte
}

// Claim requests ownership of one programmatic configuration value.
type Claim struct {
	Name  string
	Value any
}

type claimBaseline struct {
	value  any
	origin telemetry.Origin
}

type activeClaim struct {
	value   any
	holders map[uint64]Product
}

type store struct {
	mu sync.Mutex

	current       *Config
	generation    uint64
	claimRevision uint64
	nextLease     uint64
	claims        map[string]*activeClaim
	useFresh      bool
	busy          bool
}

var (
	globalStore         = newStore()
	publicationSequence atomic.Uint64
)

// loadConfigForStore is a test seam for coordinating concurrent first-use
// resolution. Tests replacing it must not run in parallel.
var loadConfigForStore = loadConfig

// Target seams let tests prove Store locks are released before compatibility
// registries are entered.
var (
	applyHeaderTagsForPublication = func(publicationID, generation, revision uint64, prepared preparedHeaderTags) bool {
		return globalconfig.ApplyHeaderTagsForPublication(
			publicationID,
			generation,
			revision,
			preparedHeaderTagMap(prepared),
		)
	}
	applyContainerTagsHashForPublication = processtags.ApplyContainerTagsHashForPublication
)

func newStore() *store {
	return &store{claims: make(map[string]*activeClaim)}
}

// PreparedClaims is an opaque claim snapshot bound to one staged Config.
type PreparedClaims struct {
	store               *store
	config              *Config
	storeClaimRevision  uint64
	configClaimRevision uint64
	claims              map[string]any
}

// Publication identifies one committed Store generation. It carries no
// coordinator ownership and requires no caller completion.
type Publication struct {
	store         *store
	config        *Config
	publicationID uint64
	generation    uint64
}

// NewTracerGeneration resolves and returns an unpublished tracer generation.
func NewTracerGeneration() *Config {
	return loadConfig()
}

// Get returns the current non-nil published configuration. On first use it
// resolves outside the store lock, then publishes with a double-check.
func Get() *Config {
	globalStore.mu.Lock()
	current := globalStore.current
	fresh := globalStore.useFresh
	busy := globalStore.busy
	globalStore.mu.Unlock()
	if current != nil && (!fresh || busy) {
		return current
	}

	resolved := loadConfigForStore()

	globalStore.mu.Lock()
	if globalStore.current != nil && !globalStore.useFresh {
		current = globalStore.current
		globalStore.mu.Unlock()
		return current
	}
	if globalStore.busy {
		current = globalStore.current
		globalStore.mu.Unlock()
		return current
	}
	old := globalStore.publishLocked(resolved, nil)
	globalStore.busy = true
	publication := Publication{
		store:         globalStore,
		config:        resolved,
		publicationID: resolved.publicationID.Load(),
		generation:    resolved.generation.Load(),
	}
	globalStore.mu.Unlock()
	defer globalStore.finishPublication(old)
	publication.ApplyContainerTagsHash(0, "")
	publication.ApplyHeaderAsTags()
	resolved.DrainPublicationTelemetry()
	return resolved
}

// CreateNew resolves and publishes a fresh baseline configuration. Tracer
// construction must use NewTracerGeneration so failures remain unpublished.
func CreateNew() *Config {
	globalStore.mu.Lock()
	if globalStore.busy {
		current := globalStore.current
		globalStore.mu.Unlock()
		return current
	}
	globalStore.mu.Unlock()

	resolved := loadConfig()
	globalStore.mu.Lock()
	if globalStore.busy {
		current := globalStore.current
		globalStore.mu.Unlock()
		return current
	}
	old := globalStore.publishLocked(resolved, nil)
	globalStore.busy = true
	publication := Publication{
		store:         globalStore,
		config:        resolved,
		publicationID: resolved.publicationID.Load(),
		generation:    resolved.generation.Load(),
	}
	globalStore.mu.Unlock()
	defer globalStore.finishPublication(old)
	publication.ApplyContainerTagsHash(0, "")
	publication.ApplyHeaderAsTags()
	resolved.DrainPublicationTelemetry()
	return resolved
}

// SetUseFreshConfig controls the legacy test mode in which each Get publishes
// a newly resolved baseline.
func SetUseFreshConfig(use bool) {
	globalStore.mu.Lock()
	globalStore.useFresh = use
	globalStore.mu.Unlock()
}

// AcquireProductClaims acquires every non-conflicting claim independently and
// reports conflicts before returning. Same-value claims may have multiple
// holders. The returned release function is idempotent and removes only the
// leases acquired by this call.
func AcquireProductClaims(product Product, claims []Claim) (release func(), accepted map[string]bool) {
	release, accepted, reportConflicts := PrepareProductClaims(product, claims)
	reportConflicts()
	return release, accepted
}

// PrepareProductClaims acquires every non-conflicting claim independently and
// returns an idempotent conflict reporter. Callers that publish runtime state
// under a lock can defer the reporter until after publication and unlock.
func PrepareProductClaims(product Product, claims []Claim) (release func(), accepted map[string]bool, reportConflicts func()) {
	claimStore := globalStore
	accepted = make(map[string]bool, len(claims))
	if product != ProductProfiler {
		for _, claim := range claims {
			if claim.Name != "" {
				accepted[claim.Name] = false
			}
		}
		return func() {}, accepted, func() {}
	}
	type requestedClaim struct {
		name  string
		value any
	}
	requested := make([]requestedClaim, 0, len(claims))
	seen := make(map[string]struct{}, len(claims))
	for _, claim := range claims {
		if claim.Name == "" {
			continue
		}
		if _, supported := shareableClaimNames[claim.Name]; !supported {
			accepted[claim.Name] = false
			continue
		}
		if _, duplicate := seen[claim.Name]; duplicate {
			continue
		}
		seen[claim.Name] = struct{}{}
		value, ok := normalizeSupportedClaimValue(claim.Name, claim.Value)
		if !ok {
			accepted[claim.Name] = false
			continue
		}
		requested = append(requested, requestedClaim{
			name:  claim.Name,
			value: value,
		})
	}

	leases := make(map[string]uint64, len(requested))
	type conflict struct {
		name  string
		first Product
	}
	conflicts := make([]conflict, 0)
	claimStore.mu.Lock()
	for _, claim := range requested {
		entry, exists := claimStore.claims[claim.name]
		if exists && !claimValuesEqual(entry.value, claim.value) {
			accepted[claim.name] = false
			conflicts = append(conflicts, conflict{
				name:  claim.name,
				first: firstClaimProduct(entry, ""),
			})
			continue
		}
		if !exists {
			entry = &activeClaim{
				value:   snapshotClaimValue(claim.value),
				holders: make(map[uint64]Product),
			}
			claimStore.claims[claim.name] = entry
		}
		claimStore.nextLease++
		lease := claimStore.nextLease
		entry.holders[lease] = product
		leases[claim.name] = lease
		accepted[claim.name] = true
		claimStore.claimRevision++
	}
	claimStore.mu.Unlock()

	var reportOnce sync.Once
	reportConflicts = func() {
		reportOnce.Do(func() {
			for _, conflict := range conflicts {
				reportProductConflict(conflict.name, conflict.first, product)
			}
		})
	}

	var once sync.Once
	return func() {
		once.Do(func() {
			claimStore.mu.Lock()
			for name, lease := range leases {
				entry, exists := claimStore.claims[name]
				if !exists {
					continue
				}
				if _, exists := entry.holders[lease]; !exists {
					continue
				}
				delete(entry.holders, lease)
				if len(entry.holders) == 0 {
					delete(claimStore.claims, name)
				}
				claimStore.claimRevision++
			}
			claimStore.mu.Unlock()
		})
	}, accepted, reportConflicts
}

// PrepareClaims reverts claims that already conflict with another active
// product and returns an immutable claim snapshot.
func (c *Config) PrepareClaims() PreparedClaims {
	type claimSnapshot struct {
		value   any
		product Product
	}
	claimStore := globalStore
	claimStore.mu.Lock()
	storeRevision := claimStore.claimRevision
	active := make(map[string]claimSnapshot, len(claimStore.claims))
	for name, entry := range claimStore.claims {
		product := firstClaimProduct(entry, ProductTracer)
		if product == "" {
			continue
		}
		active[name] = claimSnapshot{
			value:   snapshotClaimValue(entry.value),
			product: product,
		}
	}
	claimStore.mu.Unlock()

	type conflict struct {
		name  string
		first Product
	}
	conflicts := make([]conflict, 0)
	c.mu.Lock()
	conflicted := make(map[string]struct{})
	for name, claim := range c.stagedClaims {
		activeClaim, exists := active[name]
		if !exists || claimValuesEqual(activeClaim.value, claim) {
			continue
		}
		conflicted[name] = struct{}{}
		conflicts = append(conflicts, conflict{name: name, first: activeClaim.product})
	}
	for {
		added := false
		for child, parent := range c.claimDependencies {
			if _, parentConflicted := conflicted[parent]; !parentConflicted {
				continue
			}
			if _, alreadyConflicted := conflicted[child]; alreadyConflicted {
				continue
			}
			conflicted[child] = struct{}{}
			added = true
		}
		if !added {
			break
		}
	}
	for name := range conflicted {
		c.restoreClaimBaselineLocked(name)
		delete(c.stagedClaims, name)
		delete(c.claimDependencies, name)
		c.claimRevision++
	}
	prepared := PreparedClaims{
		store:               claimStore,
		config:              c,
		storeClaimRevision:  storeRevision,
		configClaimRevision: c.claimRevision,
		claims:              cloneClaimMap(c.stagedClaims),
	}
	c.mu.Unlock()

	for _, conflict := range conflicts {
		reportProductConflict(conflict.name, conflict.first, ProductTracer)
	}
	return prepared
}

// PublishTracerGeneration atomically commits a staged generation and its
// tracer claims to the Store, then runs handoff synchronously with no Store
// lock held. A committed publication always retires its predecessor and
// releases the handoff scope, including when handoff panics.
func PublishTracerGeneration(c *Config, prepared PreparedClaims, handoff func(Publication)) error {
	if c == nil || prepared.config != c || prepared.store == nil || prepared.store != globalStore {
		return errInvalidPreparedClaims
	}
	claimStore := prepared.store
	claimStore.mu.Lock()
	if claimStore.busy {
		claimStore.mu.Unlock()
		return errPublicationBusy
	}
	claimStore.mu.Unlock()

	c.mu.RLock()
	if c.published.Load() || c.claimRevision != prepared.configClaimRevision ||
		!c.claimValuesMatchLocked(prepared.claims) {
		c.mu.RUnlock()
		return errInvalidPreparedClaims
	}

	type conflict struct {
		name  string
		first Product
	}
	var conflicts []conflict
	claimStore.mu.Lock()
	if claimStore.busy {
		claimStore.mu.Unlock()
		c.mu.RUnlock()
		return errPublicationBusy
	}
	if claimStore.claimRevision != prepared.storeClaimRevision {
		for name, value := range prepared.claims {
			entry, exists := claimStore.claims[name]
			if !exists || claimCompatibleWithProduct(entry, value, ProductTracer) {
				continue
			}
			conflicts = append(conflicts, conflict{
				name:  name,
				first: firstClaimProduct(entry, ProductTracer),
			})
		}
	}
	if len(conflicts) > 0 {
		claimStore.mu.Unlock()
		c.mu.RUnlock()
		for _, conflict := range conflicts {
			reportProductConflict(conflict.name, conflict.first, ProductTracer)
		}
		return errClaimRevisionConflict
	}
	if !c.publicationStarted.CompareAndSwap(false, true) {
		claimStore.mu.Unlock()
		c.mu.RUnlock()
		return errInvalidPreparedClaims
	}

	old := claimStore.publishLocked(c, prepared.claims)
	claimStore.busy = true
	publication := Publication{
		store:         claimStore,
		config:        c,
		publicationID: c.publicationID.Load(),
		generation:    c.generation.Load(),
	}
	claimStore.mu.Unlock()
	c.mu.RUnlock()
	defer claimStore.finishPublication(old)
	defer publication.ApplyHeaderAsTags()
	publication.ApplyContainerTagsHash(0, "")
	if handoff != nil {
		handoff(publication)
	}
	return nil
}

// propagateHeaderAsTagsIfCurrent applies a dynamic update only while this
// generation owns the Store. The generation/revision fence prevents a stale
// initial snapshot or retired generation from overwriting a later value.
func (c *Config) propagateHeaderAsTagsIfCurrent(headerAsTags []string) bool {
	header, revision := c.updateHeaderSnapshot(headerAsTags)
	return globalStore.applyHeaderAsTags(
		c,
		c.publicationID.Load(),
		c.generation.Load(),
		revision,
		header,
	)
}

func (c *Config) initializeHeaderSnapshot(header []string) {
	c.headerSnapshotMu.Lock()
	c.headerSnapshotValue = append([]string(nil), header...)
	c.headerRevision = 0
	c.headerSnapshotMu.Unlock()
}

func (c *Config) updateHeaderSnapshot(header []string) ([]string, uint64) {
	c.headerSnapshotMu.Lock()
	c.headerRevision++
	c.headerSnapshotValue = append(c.headerSnapshotValue[:0], header...)
	snapshot := append([]string(nil), c.headerSnapshotValue...)
	revision := c.headerRevision
	c.headerSnapshotMu.Unlock()
	return snapshot, revision
}

func (c *Config) headerSnapshot() ([]string, uint64) {
	c.headerSnapshotMu.RLock()
	header := append([]string(nil), c.headerSnapshotValue...)
	revision := c.headerRevision
	c.headerSnapshotMu.RUnlock()
	return header, revision
}

// Config returns the generation committed by this publication.
func (p Publication) Config() *Config {
	return p.config
}

// Generation returns the Store generation assigned at commit time.
func (p Publication) Generation() uint64 {
	return p.generation
}

// IsCurrent reports whether this exact publication still owns the Store.
func (p Publication) IsCurrent() bool {
	if p.store == nil || p.config == nil || p.store != globalStore {
		return false
	}
	p.store.mu.Lock()
	current := p.store.current == p.config &&
		p.store.generation == p.generation &&
		p.config.publicationID.Load() == p.publicationID
	p.store.mu.Unlock()
	return current
}

// ApplyHeaderAsTags publishes the generation's latest header mapping through a
// monotonic generation/revision fence.
func (p Publication) ApplyHeaderAsTags() bool {
	if p.store == nil || p.config == nil || p.store != globalStore {
		return false
	}
	header, revision := p.config.headerSnapshot()
	return p.store.applyHeaderAsTags(
		p.config,
		p.publicationID,
		p.generation,
		revision,
		header,
	)
}

// ApplyContainerTagsHash publishes a process tag only while this generation is
// current and revision is newer than its last accepted update.
func (p Publication) ApplyContainerTagsHash(revision uint64, hash string) bool {
	if p.store == nil || p.config == nil || p.store != globalStore {
		return false
	}
	p.store.mu.Lock()
	current := p.store.current == p.config &&
		p.store.generation == p.generation &&
		p.config.publicationID.Load() == p.publicationID
	p.store.mu.Unlock()
	if !current {
		return false
	}
	return applyContainerTagsHashForPublication(
		p.publicationID,
		p.generation,
		revision,
		hash,
	)
}

func (s *store) applyHeaderAsTags(c *Config, publicationID, generation, revision uint64, header []string) bool {
	if c == nil || publicationID == 0 || generation == 0 || s != globalStore {
		return false
	}
	prepared := prepareHeaderAsTagsForRegistry(header)
	s.mu.Lock()
	current := s.current == c &&
		s.generation == generation &&
		c.publicationID.Load() == publicationID
	s.mu.Unlock()
	if !current {
		return false
	}
	applied := applyHeaderTagsForPublication(publicationID, generation, revision, prepared)
	if !applied {
		return false
	}
	logRejectedHeaderTags(prepared)
	return true
}

func (s *store) finishPublication(previous *Config) {
	retireGeneration(previous)
	s.mu.Lock()
	s.busy = false
	s.mu.Unlock()
}

func (s *store) publishLocked(c *Config, tracerClaims map[string]any) *Config {
	old := s.current
	claimsChanged := false
	for name, entry := range s.claims {
		for lease, product := range entry.holders {
			if product == ProductTracer {
				delete(entry.holders, lease)
				claimsChanged = true
			}
		}
		if len(entry.holders) == 0 {
			delete(s.claims, name)
		}
	}
	for name, value := range tracerClaims {
		entry, exists := s.claims[name]
		if !exists {
			entry = &activeClaim{
				value:   snapshotClaimValue(value),
				holders: make(map[uint64]Product),
			}
			s.claims[name] = entry
		}
		s.nextLease++
		entry.holders[s.nextLease] = ProductTracer
		claimsChanged = true
	}
	if claimsChanged {
		s.claimRevision++
	}
	s.generation++
	c.publicationID.Store(publicationSequence.Add(1))
	c.generation.Store(s.generation)
	c.published.Store(true)
	c.retired.Store(false)
	s.current = c
	return old
}

func retireGeneration(c *Config) {
	if c != nil {
		c.retired.Store(true)
	}
}

func claimCompatibleWithProduct(entry *activeClaim, value any, ignored Product) bool {
	if claimValuesEqual(entry.value, value) {
		return true
	}
	for _, product := range entry.holders {
		if product != ignored {
			return false
		}
	}
	return true
}

func firstClaimProduct(entry *activeClaim, ignored Product) Product {
	for _, product := range entry.holders {
		if product != ignored {
			return product
		}
	}
	return ""
}

func reportProductConflict(name string, first, second Product) {
	telemetry.Count(telemetry.NamespaceGeneral, "config.product_conflict", []string{
		"name:" + name,
		"first_product:" + string(first),
		"second_product:" + string(second),
	}).Submit(1)
	log.Warn("config: %s already set %s via programmatic API; ignoring %s's attempt to override it",
		first, name, second)
}

func (c *Config) recordStagedClaimLocked(name string, value any) {
	if _, shareable := shareableClaimNames[name]; !shareable {
		return
	}
	if _, supported := c.claimBaselines[name]; !supported {
		return
	}
	normalized, ok := normalizeSupportedClaimValue(name, value)
	if !ok {
		normalized = unsupportedClaimValue{}
	}
	if c.stagedClaims == nil {
		c.stagedClaims = make(map[string]any)
	}
	c.stagedClaims[name] = normalized
	c.claimRevision++
}

// HasStagedTracerClaim reports whether a shareable programmatic claim is
// currently staged on this generation.
func (c *Config) HasStagedTracerClaim(name string) bool {
	c.mu.RLock()
	_, ok := c.stagedClaims[name]
	c.mu.RUnlock()
	return ok
}

// DependTracerClaim records that child was derived from parent. If parent is
// reverted during claim preparation, child is reverted with it.
func (c *Config) DependTracerClaim(child, parent string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, childStaged := c.stagedClaims[child]; !childStaged {
		return
	}
	if _, parentStaged := c.stagedClaims[parent]; !parentStaged {
		return
	}
	if c.claimDependencies == nil {
		c.claimDependencies = make(map[string]string)
	}
	c.claimDependencies[child] = parent
	c.claimRevision++
}

func (c *Config) claimValuesMatchLocked(claims map[string]any) bool {
	if len(claims) != len(c.stagedClaims) {
		return false
	}
	for name, prepared := range claims {
		current, ok := c.currentClaimValueLocked(name)
		if !ok {
			return false
		}
		normalized, supported := normalizeSupportedClaimValue(name, current)
		if !supported {
			normalized = unsupportedClaimValue{}
		}
		if !claimValuesEqual(prepared, normalized) {
			return false
		}
	}
	return true
}

func (c *Config) captureClaimBaselines() {
	c.mu.Lock()
	defer c.mu.Unlock()
	globalTags, globalTagsOrigin := c.globalTags.Baseline()
	c.claimBaselines = map[string]claimBaseline{
		"DD_TRACE_AGENT_URL": {
			value: cloneURL(c.agentURL),
		},
		"DD_SERVICE": {value: c.serviceName},
		"DD_ENV":     {value: c.env},
		"DD_VERSION": {value: c.version},
		"DD_SITE":    {value: c.site},
		"DD_TAGS": {
			value:  snapshotClaimValue(globalTags),
			origin: globalTagsOrigin,
		},
	}
}

func (c *Config) restoreClaimBaselineLocked(name string) {
	baseline, ok := c.claimBaselines[name]
	if !ok {
		return
	}
	if override, exists := c.overrides[name]; exists && override.product == ProductTracer {
		delete(c.overrides, name)
	}
	switch name {
	case "DD_TRACE_AGENT_URL":
		c.agentURL = cloneURL(baseline.value.(*url.URL))
	case "DD_SERVICE":
		c.serviceName = baseline.value.(string)
	case "DD_ENV":
		c.env = baseline.value.(string)
	case "DD_VERSION":
		c.version = baseline.value.(string)
	case "DD_SITE":
		c.site = baseline.value.(string)
	case "DD_TAGS":
		c.globalTags.setBaseline(snapshotClaimValue(baseline.value).(map[string]any), baseline.origin)
		c.programmaticTags = nil
	}
}

func (c *Config) currentClaimValueLocked(name string) (any, bool) {
	switch name {
	case "DD_TRACE_AGENT_URL":
		return cloneURL(c.agentURL), true
	case "DD_SERVICE":
		return c.serviceName, true
	case "DD_ENV":
		return c.env, true
	case "DD_VERSION":
		return c.version, true
	case "DD_SITE":
		return c.site, true
	case "DD_TAGS":
		return snapshotClaimValue(c.programmaticTags), true
	default:
		return nil, false
	}
}

func normalizeSupportedClaimValue(name string, value any) (any, bool) {
	switch name {
	case "DD_SERVICE", "DD_ENV", "DD_VERSION", "DD_SITE":
		value, ok := value.(string)
		return value, ok
	case "DD_TRACE_AGENT_URL":
		canonical, ok := canonicalURLClaim(value)
		if !ok {
			return nil, false
		}
		return urlClaimFingerprint{Digest: sha256.Sum256([]byte(canonical))}, true
	case "DD_TAGS":
		return canonicalizeTagClaim(value)
	default:
		return nil, false
	}
}

func canonicalURLClaim(value any) (string, bool) {
	switch value := value.(type) {
	case nil:
		return "", true
	case *url.URL:
		if value == nil {
			return "", true
		}
		return cloneURL(value).String(), true
	case url.URL:
		return cloneURL(&value).String(), true
	case string:
		parsed, err := url.Parse(value)
		if err != nil {
			return "", false
		}
		return parsed.String(), true
	default:
		return "", false
	}
}

func canonicalizeTagClaim(value any) (any, bool) {
	tags := make(map[string]string)
	switch value := value.(type) {
	case nil:
	case map[string]string:
		if len(value) > maxClaimSnapshotNodes {
			return nil, false
		}
		maps.Copy(tags, value)
	case map[string]any:
		if len(value) > maxClaimSnapshotNodes {
			return nil, false
		}
		for key, item := range value {
			text, ok := item.(string)
			if !ok {
				return nil, false
			}
			tags[key] = text
		}
	case []string:
		if len(value) > maxClaimSnapshotNodes {
			return nil, false
		}
		for _, tag := range value {
			parts := strings.SplitN(tag, ":", 2)
			if len(parts) != 2 {
				return nil, false
			}
			tags[parts[0]] = parts[1]
		}
	default:
		return nil, false
	}
	keys := make([]string, 0, len(tags))
	for key := range tags {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	canonical := canonicalTagClaim{Entries: make([]canonicalTagEntry, 0, len(keys))}
	for _, key := range keys {
		canonical.Entries = append(canonical.Entries, canonicalTagEntry{Key: key, Value: tags[key]})
	}
	return canonical, true
}

func cloneClaimMap(values map[string]any) map[string]any {
	if values == nil {
		return nil
	}
	cloned := make(map[string]any, len(values))
	for name, value := range values {
		cloned[name] = snapshotClaimValue(value)
	}
	return cloned
}

func snapshotClaimValue(value any) any {
	budget := claimSnapshotBudget{
		remaining: maxClaimSnapshotNodes,
		active:    make(map[claimSnapshotVisit]struct{}),
	}
	snapshot, ok := snapshotClaimValueBounded(value, 0, &budget)
	if !ok {
		return unsupportedClaimValue{}
	}
	return snapshot
}

const (
	maxClaimSnapshotDepth = 32
	maxClaimSnapshotNodes = 1024
)

type claimSnapshotVisit struct {
	kind reflect.Kind
	ptr  uintptr
}

type claimSnapshotBudget struct {
	remaining int
	active    map[claimSnapshotVisit]struct{}
}

func snapshotClaimValueBounded(value any, depth int, budget *claimSnapshotBudget) (any, bool) {
	if depth > maxClaimSnapshotDepth || budget.remaining == 0 {
		return nil, false
	}
	budget.remaining--
	switch value := value.(type) {
	case nil,
		string, bool,
		int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64:
		return value, true
	case *url.URL:
		return cloneURL(value), true
	case []byte:
		return append([]byte(nil), value...), true
	case []string:
		return append([]string(nil), value...), true
	case map[string]string:
		return maps.Clone(value), true
	case canonicalTagClaim:
		return canonicalTagClaim{Entries: append([]canonicalTagEntry(nil), value.Entries...)}, true
	case map[string]any:
		visit := claimSnapshotVisit{kind: reflect.Map, ptr: reflect.ValueOf(value).Pointer()}
		if _, cyclic := budget.active[visit]; cyclic {
			return nil, false
		}
		budget.active[visit] = struct{}{}
		defer delete(budget.active, visit)
		cloned := make(map[string]any, len(value))
		for key, item := range value {
			snapshot, ok := snapshotClaimValueBounded(item, depth+1, budget)
			if !ok {
				return nil, false
			}
			cloned[key] = snapshot
		}
		return cloned, true
	case []any:
		visit := claimSnapshotVisit{kind: reflect.Slice, ptr: reflect.ValueOf(value).Pointer()}
		if _, cyclic := budget.active[visit]; cyclic {
			return nil, false
		}
		budget.active[visit] = struct{}{}
		defer delete(budget.active, visit)
		cloned := make([]any, len(value))
		for i, item := range value {
			snapshot, ok := snapshotClaimValueBounded(item, depth+1, budget)
			if !ok {
				return nil, false
			}
			cloned[i] = snapshot
		}
		return cloned, true
	default:
		return value, true
	}
}

func claimValuesEqual(left, right any) bool {
	switch left := left.(type) {
	case float32:
		right, ok := right.(float32)
		return ok && (left == right || (math.IsNaN(float64(left)) && math.IsNaN(float64(right))))
	case float64:
		right, ok := right.(float64)
		return ok && (left == right || (math.IsNaN(left) && math.IsNaN(right)))
	default:
		return reflect.DeepEqual(left, right)
	}
}

func cloneURL(value *url.URL) *url.URL {
	if value == nil {
		return nil
	}
	cloned := *value
	if value.User != nil {
		user := *value.User
		cloned.User = &user
	}
	return &cloned
}
