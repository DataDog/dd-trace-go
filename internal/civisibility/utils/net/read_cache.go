// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package net

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	"github.com/DataDog/dd-trace-go/v2/internal/locking"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

const (
	// readCacheSchema identifies the on-disk cache entry format.
	readCacheSchema = "civisibility-read-cache-v1"

	// readCacheEndpointVersion is bumped when endpoint request semantics change.
	readCacheEndpointVersion = 1
	// readCacheEndpointSkippableTestsVersion is bumped independently because skippable-tests
	// cache entries include backend coverage metadata and safety state.
	readCacheEndpointSkippableTestsVersion = 2

	// readCacheScopeLocal scopes unidentified local runs by parent process.
	readCacheScopeLocal = "local"
	// readCacheScopeCIWeak scopes CI runs by provider plus pipeline identity.
	readCacheScopeCIWeak = "ci_weak"
	// readCacheScopeCIMedium scopes CI runs by provider, pipeline, and job-like names.
	readCacheScopeCIMedium = "ci_medium"
	// readCacheScopeCIStrong scopes CI runs by provider and stable job id.
	readCacheScopeCIStrong = "ci_strong"

	// readCacheEndpointSettings names the settings cache endpoint.
	readCacheEndpointSettings = "settings"
	// readCacheEndpointKnownTests names the known-tests cache endpoint.
	readCacheEndpointKnownTests = "known_tests"
	// readCacheEndpointSkippableTests names the skippable-tests cache endpoint.
	readCacheEndpointSkippableTests = "skippable_tests"
	// readCacheEndpointTestManagementTests names the test-management cache endpoint.
	readCacheEndpointTestManagementTests = "test_management_tests"

	// readCacheWaiterInitialBackoff is the first waiter poll interval.
	readCacheWaiterInitialBackoff = 20 * time.Millisecond
	// readCacheWaiterMaxBackoff caps waiter polling backoff.
	readCacheWaiterMaxBackoff = 100 * time.Millisecond
	// readCacheWaiterTimeout is how long waiters wait for another process to fill the cache.
	readCacheWaiterTimeout = 2 * time.Second
	// readCacheStaleLockTimeout is intentionally much longer than the HTTP request path.
	readCacheStaleLockTimeout = time.Hour
	// readCacheGlobalCleanupMaxEntries bounds opportunistic directory cleanup.
	readCacheGlobalCleanupMaxEntries = 32
	// readCacheGlobalCleanupMaxDuration bounds opportunistic directory cleanup.
	readCacheGlobalCleanupMaxDuration = 5 * time.Millisecond
)

type (
	// readCacheScope is the stable base scope shared by every cacheable read endpoint.
	readCacheScope struct {
		Schema              string             `json:"schema"`
		RepositoryURLHash   string             `json:"repo_url_hash"`
		CommitSHA           string             `json:"commit_sha"`
		Branch              string             `json:"branch"`
		Service             string             `json:"service"`
		Env                 string             `json:"env"`
		TestConfigurations  testConfigurations `json:"test_configurations"`
		Agentless           bool               `json:"agentless"`
		BaseURLHash         string             `json:"base_url_hash"`
		BaseURLSanitized    string             `json:"base_url_sanitized"`
		ScopeKind           string             `json:"scope_kind"`
		ScopeIdentityHash   string             `json:"scope_identity_hash"`
		ScopeIdentitySource []string           `json:"scope_identity_source"`
	}

	// readCacheScopeIdentity is selected once during client construction from already-resolved CI tags.
	readCacheScopeIdentity struct {
		Kind   string
		Source []string
		Hash   string
		TTL    time.Duration
	}

	// readCacheEndpointScope identifies the cacheable endpoint and semantic request payload.
	readCacheEndpointScope struct {
		Endpoint        string `json:"endpoint"`
		EndpointVersion int    `json:"endpoint_version"`
		RequestHash     string `json:"request_hash"`
	}

	// readCacheEntry stores one typed cached response plus validation metadata.
	readCacheEntry[T any] struct {
		CacheKey          string                 `json:"cache_key"`
		CreatedAtUnixNano int64                  `json:"created_at_unix_nano"`
		TTLSeconds        int64                  `json:"ttl_seconds"`
		BaseScope         readCacheScope         `json:"base_scope"`
		EndpointScope     readCacheEndpointScope `json:"endpoint_scope"`
		Response          T                      `json:"response"`
	}

	// readCacheLiveResult carries a live endpoint response and explicit cacheability.
	readCacheLiveResult[T any] struct {
		Value     T
		Cacheable bool
	}

	// readCacheIdentityValue is the canonical name/value form hashed for scope identity.
	readCacheIdentityValue struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	}

	readCachePaths struct {
		Dir       string
		CacheFile string
		LockFile  string
		TmpGlob   string
	}

	readCacheLockEntry struct {
		PID               int    `json:"pid"`
		CreatedAtUnixNano int64  `json:"created_at_unix_nano"`
		CacheKey          string `json:"cache_key"`
		OwnerNonce        string `json:"owner_nonce"`
	}

	readCacheLockOwner struct {
		Path  string
		Entry readCacheLockEntry
	}

	readCacheLockAcquireStatus int

	readCacheHooks struct {
		cacheRoot string
		now       func() time.Time
		pid       func() int
		parentPID func() int
		sleep     func(time.Duration)
	}
)

const (
	readCacheLockAcquired readCacheLockAcquireStatus = iota
	readCacheLockActive
	readCacheLockBypass
)

var (
	readCacheHooksMu locking.RWMutex
	readCacheState   readCacheHooks
)

// SetReadCacheHooksForTesting overrides read-cache process hooks for internal tests only.
func SetReadCacheHooksForTesting(cacheRoot string, now func() time.Time, pid func() int, parentPID func() int, sleep func(time.Duration)) {
	readCacheHooksMu.Lock()
	defer readCacheHooksMu.Unlock()

	readCacheState = readCacheHooks{
		cacheRoot: cacheRoot,
		now:       now,
		pid:       pid,
		parentPID: parentPID,
		sleep:     sleep,
	}
}

// ResetReadCacheHooksForTesting restores production read-cache hooks after internal tests.
func ResetReadCacheHooksForTesting() {
	readCacheHooksMu.Lock()
	defer readCacheHooksMu.Unlock()

	readCacheState = readCacheHooks{}
}

func newReadCacheScopeIdentity(ciTags map[string]string) readCacheScopeIdentity {
	provider := readCacheTagValue(ciTags, constants.CIProviderName)
	pipelineName, pipelineValue := readCacheSelectedPipeline(ciTags)
	jobID := readCacheTagValue(ciTags, constants.CIJobID)
	jobName := readCacheTagValue(ciTags, constants.CIJobName)
	stageName := readCacheTagValue(ciTags, constants.CIStageName)
	nodeName := readCacheTagValue(ciTags, constants.CINodeName)

	if provider != "" && jobID != "" {
		values := []readCacheIdentityValue{{Name: constants.CIProviderName, Value: provider}}
		if pipelineValue != "" {
			values = append(values, readCacheIdentityValue{Name: pipelineName, Value: pipelineValue})
		}
		values = append(values, readCacheIdentityValue{Name: constants.CIJobID, Value: jobID})
		return newReadCacheScopeIdentityFromValues(readCacheScopeCIStrong, time.Hour, values)
	}

	if provider != "" && pipelineValue != "" && (jobName != "" || stageName != "" || nodeName != "") {
		values := []readCacheIdentityValue{
			{Name: constants.CIProviderName, Value: provider},
			{Name: pipelineName, Value: pipelineValue},
		}
		if jobName != "" {
			values = append(values, readCacheIdentityValue{Name: constants.CIJobName, Value: jobName})
		}
		if stageName != "" {
			values = append(values, readCacheIdentityValue{Name: constants.CIStageName, Value: stageName})
		}
		if nodeName != "" {
			values = append(values, readCacheIdentityValue{Name: constants.CINodeName, Value: nodeName})
		}
		return newReadCacheScopeIdentityFromValues(readCacheScopeCIMedium, 15*time.Minute, values)
	}

	if provider != "" && pipelineValue != "" {
		values := []readCacheIdentityValue{
			{Name: constants.CIProviderName, Value: provider},
			{Name: pipelineName, Value: pipelineValue},
		}
		return newReadCacheScopeIdentityFromValues(readCacheScopeCIWeak, 5*time.Minute, values)
	}

	values := []readCacheIdentityValue{{Name: "parent_pid", Value: fmt.Sprint(readCacheParentPID())}}
	return newReadCacheScopeIdentityFromValues(readCacheScopeLocal, time.Minute, values)
}

func newReadCacheScopeIdentityFromValues(kind string, ttl time.Duration, values []readCacheIdentityValue) readCacheScopeIdentity {
	return readCacheScopeIdentity{
		Kind:   kind,
		Source: readCacheIdentitySources(values),
		Hash:   readCacheHashValue(values),
		TTL:    ttl,
	}
}

func readCacheIdentitySources(values []readCacheIdentityValue) []string {
	sources := make([]string, 0, len(values))
	for _, value := range values {
		sources = append(sources, value.Name)
	}
	return sources
}

func readCacheSelectedPipeline(ciTags map[string]string) (string, string) {
	if pipelineID := readCacheTagValue(ciTags, constants.CIPipelineID); pipelineID != "" {
		return constants.CIPipelineID, pipelineID
	}
	if pipelineNumber := readCacheTagValue(ciTags, constants.CIPipelineNumber); pipelineNumber != "" {
		return constants.CIPipelineNumber, pipelineNumber
	}
	return "", ""
}

func readCacheTagValue(ciTags map[string]string, name string) string {
	return strings.TrimSpace(ciTags[name])
}

func (c *client) readCacheBaseScope() readCacheScope {
	identity := c.readCacheScopeIdentity
	return readCacheScope{
		Schema:              readCacheSchema,
		RepositoryURLHash:   readCacheHashString(c.repositoryURL),
		CommitSHA:           c.commitSha,
		Branch:              c.branchName,
		Service:             c.serviceName,
		Env:                 c.environment,
		TestConfigurations:  c.testConfigurations,
		Agentless:           c.agentless,
		BaseURLHash:         readCacheHashString(c.baseURL),
		BaseURLSanitized:    readCacheSanitizeBaseURL(c.baseURL),
		ScopeKind:           identity.Kind,
		ScopeIdentityHash:   identity.Hash,
		ScopeIdentitySource: append([]string(nil), identity.Source...),
	}
}

func readThroughShortLivedCache[T any](
	c *client,
	endpoint string,
	semanticRequest any,
	live func() (readCacheLiveResult[T], error),
	onCacheHit func(T),
) (T, error) {
	var zero T

	baseScope := c.readCacheBaseScope()
	requestHash, err := readCacheHashJSON(semanticRequest)
	if err != nil {
		log.Debug("civisibility.read_cache: request hash failed [endpoint:%s error:%s]", endpoint, err.Error())
		return readCacheLiveValue(live)
	}
	endpointScope := readCacheEndpointScope{
		Endpoint:        endpoint,
		EndpointVersion: readCacheEndpointVersionFor(endpoint),
		RequestHash:     requestHash,
	}
	cacheKey, err := readCacheKey(baseScope, endpointScope)
	if err != nil {
		log.Debug("civisibility.read_cache: key build failed [endpoint:%s error:%s]", endpoint, err.Error())
		return readCacheLiveValue(live)
	}

	paths, err := readCachePathsForKey(cacheKey)
	if err != nil {
		log.Debug("civisibility.read_cache: cache path unavailable [endpoint:%s key:%s error:%s]", endpoint, cacheKey, err.Error())
		return readCacheLiveValue(live)
	}
	readCacheCleanupCurrentKey(paths)

	if value, ok := readFreshReadCacheEntry[T](paths, cacheKey, baseScope, endpointScope, c.readCacheScopeIdentity.TTL); ok {
		log.Debug("civisibility.read_cache: hit [endpoint:%s key:%s]", endpoint, cacheKey)
		if onCacheHit != nil {
			onCacheHit(value)
		}
		return value, nil
	}

	owner, status := acquireReadCacheLock(paths, cacheKey)
	switch status {
	case readCacheLockAcquired:
		defer releaseReadCacheLock(owner)
		readCacheGlobalCleanup(paths.Dir)

		if value, ok := readFreshReadCacheEntry[T](paths, cacheKey, baseScope, endpointScope, c.readCacheScopeIdentity.TTL); ok {
			log.Debug("civisibility.read_cache: hit [endpoint:%s key:%s]", endpoint, cacheKey)
			if onCacheHit != nil {
				onCacheHit(value)
			}
			return value, nil
		}

		result, err := live()
		if err != nil {
			return zero, err
		}
		if result.Cacheable {
			writeReadCacheEntry(paths, owner, cacheKey, baseScope, endpointScope, c.readCacheScopeIdentity.TTL, result.Value)
		}
		return result.Value, nil
	case readCacheLockActive:
		if value, ok := waitForReadCacheEntry[T](paths, cacheKey, baseScope, endpointScope, c.readCacheScopeIdentity.TTL); ok {
			log.Debug("civisibility.read_cache: hit [endpoint:%s key:%s]", endpoint, cacheKey)
			if onCacheHit != nil {
				onCacheHit(value)
			}
			return value, nil
		}
		log.Debug("civisibility.read_cache: waiter fallback [endpoint:%s key:%s]", endpoint, cacheKey)
		return readCacheLiveValue(live)
	default:
		log.Debug("civisibility.read_cache: lock bypass [endpoint:%s key:%s]", endpoint, cacheKey)
		return readCacheLiveValue(live)
	}
}

func readCacheEndpointVersionFor(endpoint string) int {
	if endpoint == readCacheEndpointSkippableTests {
		return readCacheEndpointSkippableTestsVersion
	}
	return readCacheEndpointVersion
}

func readCacheLiveValue[T any](live func() (readCacheLiveResult[T], error)) (T, error) {
	result, err := live()
	return result.Value, err
}

func readCacheHashJSON(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func readCacheHashValue(value any) string {
	hash, err := readCacheHashJSON(value)
	if err != nil {
		return readCacheHashString("")
	}
	return hash
}

func readCacheHashString(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func readCacheKey(baseScope readCacheScope, endpointScope readCacheEndpointScope) (string, error) {
	base, err := json.Marshal(baseScope)
	if err != nil {
		return "", err
	}
	endpoint, err := json.Marshal(endpointScope)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(append(append(base, '\n'), endpoint...))
	return hex.EncodeToString(sum[:]), nil
}

func readCacheSanitizeBaseURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	parsed.User = nil
	parsed.Path = ""
	parsed.RawPath = ""
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

func readCachePathsForKey(cacheKey string) (readCachePaths, error) {
	root, err := readCacheBaseRoot()
	if err != nil {
		return readCachePaths{}, err
	}
	dir := filepath.Join(root, "dd-trace-go", "civisibility-read-cache")
	if err := ensureReadCacheDir(dir); err != nil {
		return readCachePaths{}, err
	}
	return readCachePaths{
		Dir:       dir,
		CacheFile: filepath.Join(dir, cacheKey+".json"),
		LockFile:  filepath.Join(dir, cacheKey+".lock"),
		TmpGlob:   filepath.Join(dir, cacheKey+".tmp.*"),
	}, nil
}

func readCacheBaseRoot() (string, error) {
	if root := readCacheHookSnapshot().cacheRoot; root != "" {
		return root, nil
	}
	root, err := os.UserCacheDir()
	if err == nil && root != "" {
		return root, nil
	}
	if root := os.TempDir(); root != "" {
		return root, nil
	}
	return "", errors.New("no cache root available")
}

func ensureReadCacheDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	info, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}
	if runtime.GOOS == "windows" {
		return nil
	}
	if info.Mode().Perm()&0o077 == 0 {
		return nil
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	info, err = os.Stat(dir)
	if err != nil {
		return err
	}
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("%s is not private", dir)
	}
	return nil
}

func readFreshReadCacheEntry[T any](paths readCachePaths, cacheKey string, baseScope readCacheScope, endpointScope readCacheEndpointScope, ttl time.Duration) (T, bool) {
	var zero T

	raw, err := os.ReadFile(paths.CacheFile)
	if err != nil {
		return zero, false
	}

	var entry readCacheEntry[T]
	if err := json.Unmarshal(raw, &entry); err != nil {
		log.Debug("civisibility.read_cache: invalid entry [key:%s error:%s]", cacheKey, err.Error())
		removeReadCacheFile(paths.CacheFile)
		return zero, false
	}
	if !readCacheEntryValid(entry, cacheKey, baseScope, endpointScope, ttl) {
		removeReadCacheFile(paths.CacheFile)
		return zero, false
	}
	return entry.Response, true
}

func readCacheEntryValid[T any](entry readCacheEntry[T], cacheKey string, baseScope readCacheScope, endpointScope readCacheEndpointScope, ttl time.Duration) bool {
	if entry.CacheKey != cacheKey {
		return false
	}
	if entry.TTLSeconds != int64(ttl.Seconds()) {
		return false
	}
	if !reflect.DeepEqual(entry.BaseScope, baseScope) {
		return false
	}
	if !reflect.DeepEqual(entry.EndpointScope, endpointScope) {
		return false
	}

	now := readCacheNow()
	createdAt := time.Unix(0, entry.CreatedAtUnixNano)
	if createdAt.After(now) {
		return false
	}
	return now.Sub(createdAt) <= ttl
}

func writeReadCacheEntry[T any](paths readCachePaths, owner *readCacheLockOwner, cacheKey string, baseScope readCacheScope, endpointScope readCacheEndpointScope, ttl time.Duration, value T) {
	if owner == nil || !readCacheLockOwnerStillValid(owner) {
		return
	}

	entry := readCacheEntry[T]{
		CacheKey:          cacheKey,
		CreatedAtUnixNano: readCacheNow().UnixNano(),
		TTLSeconds:        int64(ttl.Seconds()),
		BaseScope:         baseScope,
		EndpointScope:     endpointScope,
		Response:          value,
	}
	raw, err := json.Marshal(entry)
	if err != nil {
		log.Debug("civisibility.read_cache: marshal failed [key:%s error:%s]", cacheKey, err.Error())
		return
	}

	tmpPath := filepath.Join(paths.Dir, fmt.Sprintf("%s.tmp.%d.%s", cacheKey, readCachePID(), owner.Entry.OwnerNonce))
	file, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		log.Debug("civisibility.read_cache: temp create failed [key:%s error:%s]", cacheKey, err.Error())
		return
	}
	defer removeReadCacheFile(tmpPath)

	if _, err := file.Write(raw); err != nil {
		_ = file.Close()
		log.Debug("civisibility.read_cache: temp write failed [key:%s error:%s]", cacheKey, err.Error())
		return
	}
	if err := file.Close(); err != nil {
		log.Debug("civisibility.read_cache: temp close failed [key:%s error:%s]", cacheKey, err.Error())
		return
	}
	if !readCacheLockOwnerStillValid(owner) {
		return
	}
	if err := os.Rename(tmpPath, paths.CacheFile); err != nil {
		removeReadCacheFile(paths.CacheFile)
		if retryErr := os.Rename(tmpPath, paths.CacheFile); retryErr != nil {
			log.Debug("civisibility.read_cache: rename failed [key:%s error:%s]", cacheKey, retryErr.Error())
			return
		}
	}
}

func acquireReadCacheLock(paths readCachePaths, cacheKey string) (*readCacheLockOwner, readCacheLockAcquireStatus) {
	owner, err := newReadCacheLockOwner(paths.LockFile, cacheKey)
	if err != nil {
		log.Debug("civisibility.read_cache: lock nonce failed [key:%s error:%s]", cacheKey, err.Error())
		return nil, readCacheLockBypass
	}
	if status := tryCreateReadCacheLock(owner); status == readCacheLockAcquired {
		return owner, status
	} else if status == readCacheLockBypass {
		return nil, status
	}

	stale, ok := readCacheLockIsStale(paths.LockFile)
	if !stale {
		return nil, readCacheLockActive
	}
	if !ok {
		log.Debug("civisibility.read_cache: stale malformed lock [key:%s]", cacheKey)
	}
	removeReadCacheFile(paths.LockFile)
	if status := tryCreateReadCacheLock(owner); status == readCacheLockAcquired {
		return owner, status
	}
	return nil, readCacheLockBypass
}

func newReadCacheLockOwner(lockPath string, cacheKey string) (*readCacheLockOwner, error) {
	nonce, err := readCacheNonce()
	if err != nil {
		return nil, err
	}
	return &readCacheLockOwner{
		Path: lockPath,
		Entry: readCacheLockEntry{
			PID:               readCachePID(),
			CreatedAtUnixNano: readCacheNow().UnixNano(),
			CacheKey:          cacheKey,
			OwnerNonce:        nonce,
		},
	}, nil
}

func tryCreateReadCacheLock(owner *readCacheLockOwner) readCacheLockAcquireStatus {
	raw, err := json.Marshal(owner.Entry)
	if err != nil {
		return readCacheLockBypass
	}
	file, err := os.OpenFile(owner.Path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		if os.IsExist(err) {
			return readCacheLockActive
		}
		log.Debug("civisibility.read_cache: lock create failed [key:%s error:%s]", owner.Entry.CacheKey, err.Error())
		return readCacheLockBypass
	}
	if _, err := file.Write(raw); err != nil {
		_ = file.Close()
		removeReadCacheFile(owner.Path)
		log.Debug("civisibility.read_cache: lock write failed [key:%s error:%s]", owner.Entry.CacheKey, err.Error())
		return readCacheLockBypass
	}
	if err := file.Close(); err != nil {
		removeReadCacheFile(owner.Path)
		log.Debug("civisibility.read_cache: lock close failed [key:%s error:%s]", owner.Entry.CacheKey, err.Error())
		return readCacheLockBypass
	}
	return readCacheLockAcquired
}

func waitForReadCacheEntry[T any](paths readCachePaths, cacheKey string, baseScope readCacheScope, endpointScope readCacheEndpointScope, ttl time.Duration) (T, bool) {
	var zero T

	if value, ok := readFreshReadCacheEntry[T](paths, cacheKey, baseScope, endpointScope, ttl); ok {
		return value, true
	}

	deadline := readCacheNow().Add(readCacheWaiterTimeout)
	backoff := readCacheWaiterInitialBackoff
	for readCacheNow().Before(deadline) {
		if _, err := os.Stat(paths.LockFile); os.IsNotExist(err) {
			if value, ok := readFreshReadCacheEntry[T](paths, cacheKey, baseScope, endpointScope, ttl); ok {
				return value, true
			}
			return zero, false
		}

		readCacheSleep(backoff)
		if value, ok := readFreshReadCacheEntry[T](paths, cacheKey, baseScope, endpointScope, ttl); ok {
			return value, true
		}
		if backoff < readCacheWaiterMaxBackoff {
			backoff *= 2
			if backoff > readCacheWaiterMaxBackoff {
				backoff = readCacheWaiterMaxBackoff
			}
		}
	}
	return zero, false
}

func readCacheLockOwnerStillValid(owner *readCacheLockOwner) bool {
	if owner == nil {
		return false
	}
	current, err := readCacheLockEntryFromFile(owner.Path)
	if err != nil {
		return false
	}
	return current.CacheKey == owner.Entry.CacheKey && current.OwnerNonce == owner.Entry.OwnerNonce
}

func releaseReadCacheLock(owner *readCacheLockOwner) {
	if !readCacheLockOwnerStillValid(owner) {
		return
	}
	removeReadCacheFile(owner.Path)
}

func readCacheLockIsStale(lockPath string) (stale bool, decoded bool) {
	lockEntry, err := readCacheLockEntryFromFile(lockPath)
	if err == nil {
		return readCacheNow().Sub(time.Unix(0, lockEntry.CreatedAtUnixNano)) > readCacheStaleLockTimeout, true
	}
	info, statErr := os.Stat(lockPath)
	if statErr != nil {
		return false, false
	}
	return readCacheNow().Sub(info.ModTime()) > readCacheStaleLockTimeout, false
}

func readCacheLockEntryFromFile(lockPath string) (readCacheLockEntry, error) {
	raw, err := os.ReadFile(lockPath)
	if err != nil {
		return readCacheLockEntry{}, err
	}
	var lockEntry readCacheLockEntry
	if err := json.Unmarshal(raw, &lockEntry); err != nil {
		return readCacheLockEntry{}, err
	}
	if lockEntry.OwnerNonce == "" {
		return readCacheLockEntry{}, errors.New("lock missing owner nonce")
	}
	return lockEntry, nil
}

func readCacheCleanupCurrentKey(paths readCachePaths) {
	stale, _ := readCacheLockIsStale(paths.LockFile)
	if stale {
		removeReadCacheFile(paths.LockFile)
	}

	matches, err := filepath.Glob(paths.TmpGlob)
	if err != nil {
		return
	}
	for _, path := range matches {
		if readCachePathOlderThan(path, readCacheStaleLockTimeout) {
			removeReadCacheFile(path)
		}
	}
}

func readCacheGlobalCleanup(dir string) {
	start := readCacheNow()
	dirFile, err := os.Open(dir)
	if err != nil {
		log.Debug("civisibility.read_cache: global cleanup read failed [error:%s]", err.Error())
		return
	}
	defer dirFile.Close()

	entries, err := dirFile.ReadDir(readCacheGlobalCleanupMaxEntries)
	if err != nil && !errors.Is(err, io.EOF) {
		log.Debug("civisibility.read_cache: global cleanup read failed [error:%s]", err.Error())
		return
	}
	for i, entry := range entries {
		if i >= readCacheGlobalCleanupMaxEntries || readCacheNow().Sub(start) > readCacheGlobalCleanupMaxDuration {
			return
		}
		path := filepath.Join(dir, entry.Name())
		switch {
		case strings.HasSuffix(entry.Name(), ".json"):
			if readCacheJSONFileExpired(path) {
				removeReadCacheFile(path)
			}
		case strings.HasSuffix(entry.Name(), ".lock"):
			if stale, _ := readCacheLockIsStale(path); stale {
				removeReadCacheFile(path)
			}
		case strings.Contains(entry.Name(), ".tmp."):
			if readCachePathOlderThan(path, readCacheStaleLockTimeout) {
				removeReadCacheFile(path)
			}
		}
	}
}

func readCacheJSONFileExpired(path string) bool {
	raw, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var envelope struct {
		CreatedAtUnixNano int64 `json:"created_at_unix_nano"`
		TTLSeconds        int64 `json:"ttl_seconds"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return false
	}
	if envelope.CreatedAtUnixNano <= 0 || envelope.TTLSeconds <= 0 {
		return false
	}
	return readCacheNow().Sub(time.Unix(0, envelope.CreatedAtUnixNano)) > time.Duration(envelope.TTLSeconds)*time.Second
}

func readCachePathOlderThan(path string, maxAge time.Duration) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return readCacheNow().Sub(info.ModTime()) > maxAge
}

func removeReadCacheFile(path string) {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		log.Debug("civisibility.read_cache: remove failed [path:%s error:%s]", filepath.Base(path), err.Error())
	}
}

func readCacheNonce() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func readCacheHookSnapshot() readCacheHooks {
	readCacheHooksMu.RLock()
	defer readCacheHooksMu.RUnlock()

	return readCacheState
}

func readCacheNow() time.Time {
	if now := readCacheHookSnapshot().now; now != nil {
		return now()
	}
	return time.Now()
}

func readCachePID() int {
	if pid := readCacheHookSnapshot().pid; pid != nil {
		return pid()
	}
	return os.Getpid()
}

func readCacheParentPID() int {
	if parentPID := readCacheHookSnapshot().parentPID; parentPID != nil {
		return parentPID()
	}
	return os.Getppid()
}

func readCacheSleep(duration time.Duration) {
	if sleep := readCacheHookSnapshot().sleep; sleep != nil {
		sleep(duration)
		return
	}
	time.Sleep(duration)
}
