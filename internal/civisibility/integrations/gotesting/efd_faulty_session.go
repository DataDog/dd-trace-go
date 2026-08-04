// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gotesting

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/bits"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal/bazel"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	civisibilitynet "github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils/net"
	"github.com/DataDog/dd-trace-go/v2/internal/locking"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

const (
	efdFaultySessionDirectoryName = ".dd-civisibility-efd-session"
	efdFaultySessionStateFile     = "state.json"
	efdFaultySessionCountFile     = "count"
	efdFaultySessionTerminalFile  = "terminal"
	efdFaultySessionLockFile      = "lock"
	efdFaultySessionSchemaVersion = 1
	efdFaultySessionMaxStateBytes = 1024

	efdFaultySessionInitialBackoff = 250 * time.Microsecond
	efdFaultySessionMaxBackoff     = 10 * time.Millisecond
	efdFaultySessionLockTimeout    = 2 * time.Second
)

type earlyFlakeDetectionAdmission uint8

const (
	earlyFlakeDetectionAdmissionAllowed earlyFlakeDetectionAdmission = iota
	earlyFlakeDetectionAdmissionFaulty
	earlyFlakeDetectionAdmissionUnavailable
)

type earlyFlakeDetectionFaultySession interface {
	admitNewTest(*testIdentity) earlyFlakeDetectionAdmission
	retryState() earlyFlakeDetectionAdmission
}

type efdFaultySessionIdentity struct {
	module string
	suite  string
	test   string
}

type efdFaultySessionIdentityEntry struct {
	done      chan struct{}
	admission earlyFlakeDetectionAdmission
}

type efdFaultySessionStore interface {
	claim() earlyFlakeDetectionAdmission
	observe() (earlyFlakeDetectionAdmission, bool)
}

type efdFaultySessionGuard struct {
	terminal   atomic.Uint32
	mu         locking.Mutex
	identities map[efdFaultySessionIdentity]*efdFaultySessionIdentityEntry
	store      efdFaultySessionStore
}

type efdFaultySessionState struct {
	Version   int    `json:"version"`
	Threshold uint64 `json:"threshold"`
	Known     uint64 `json:"known"`
	Limit     uint64 `json:"limit"`
}

type efdFaultySessionFilesystemStore struct {
	directory      string
	threshold      uint64
	knownCount     func() uint64
	now            func() time.Time
	sleep          func(time.Duration)
	stateWriter    func(string, efdFaultySessionState) error
	terminalWriter func(string) error
}

type efdFaultySessionLocalStore struct {
	threshold  uint64
	knownCount func() uint64
	initOnce   sync.Once
	mu         locking.Mutex
	limit      uint64
	count      uint64
	terminal   atomic.Uint32
}

func newEarlyFlakeDetectionFaultySession(
	settings *civisibilitynet.SettingsResponseData,
	knownTests *civisibilitynet.KnownTestsResponseData,
) earlyFlakeDetectionFaultySession {
	if settings == nil || !settings.EarlyFlakeDetection.Enabled {
		return nil
	}
	threshold := settings.EarlyFlakeDetection.FaultySessionThreshold
	if threshold == nil || *threshold == 100 {
		return nil
	}
	if *threshold < 0 || *threshold > 100 {
		log.Debug("civisibility: invalid EFD faulty-session threshold %d; suppressing EFD retries", *threshold)
		return newTerminalEFDGuard(earlyFlakeDetectionAdmissionUnavailable)
	}
	if *threshold != 0 && (knownTests == nil || len(knownTests.Tests) == 0) {
		return nil
	}

	knownCount := func() uint64 { return countUniqueKnownTopLevelTests(knownTests) }
	var store efdFaultySessionStore
	if root, ok := efdFaultySessionInvocationRoot(); ok && !bazel.IsManifestModeEnabled() && !bazel.IsPayloadFilesModeEnabled() {
		store = &efdFaultySessionFilesystemStore{
			directory:  filepath.Join(root, efdFaultySessionDirectoryName),
			threshold:  uint64(*threshold),
			knownCount: knownCount,
			now:        time.Now,
			sleep:      time.Sleep,
		}
	} else {
		store = &efdFaultySessionLocalStore{threshold: uint64(*threshold), knownCount: knownCount}
	}
	return &efdFaultySessionGuard{
		identities: make(map[efdFaultySessionIdentity]*efdFaultySessionIdentityEntry),
		store:      store,
	}
}

func newTerminalEFDGuard(admission earlyFlakeDetectionAdmission) *efdFaultySessionGuard {
	guard := &efdFaultySessionGuard{}
	guard.terminal.Store(uint32(admission))
	return guard
}

func (g *efdFaultySessionGuard) admitNewTest(identity *testIdentity) earlyFlakeDetectionAdmission {
	if g == nil {
		return earlyFlakeDetectionAdmissionAllowed
	}
	if terminal := g.localTerminal(); terminal != earlyFlakeDetectionAdmissionAllowed {
		return terminal
	}
	if identity == nil || identity.FullName == "" || len(identity.Segments) != 1 {
		return earlyFlakeDetectionAdmissionUnavailable
	}
	key := efdFaultySessionIdentity{module: identity.ModuleName, suite: identity.SuiteName, test: identity.BaseName}

	g.mu.Lock()
	entry := g.identities[key]
	if entry == nil {
		entry = &efdFaultySessionIdentityEntry{done: make(chan struct{})}
		g.identities[key] = entry
		g.mu.Unlock()
		entry.admission = g.store.claim()
		close(entry.done)
	} else {
		g.mu.Unlock()
		<-entry.done
	}

	if entry.admission != earlyFlakeDetectionAdmissionAllowed {
		g.cacheTerminal(entry.admission)
		return entry.admission
	}
	return g.retryState()
}

func (g *efdFaultySessionGuard) retryState() earlyFlakeDetectionAdmission {
	if g == nil {
		return earlyFlakeDetectionAdmissionAllowed
	}
	if terminal := g.localTerminal(); terminal != earlyFlakeDetectionAdmissionAllowed {
		return terminal
	}
	if g.store == nil {
		return earlyFlakeDetectionAdmissionAllowed
	}
	admission, cache := g.store.observe()
	if cache {
		g.cacheTerminal(admission)
	}
	return admission
}

func (g *efdFaultySessionGuard) localTerminal() earlyFlakeDetectionAdmission {
	return earlyFlakeDetectionAdmission(g.terminal.Load())
}

func (g *efdFaultySessionGuard) cacheTerminal(admission earlyFlakeDetectionAdmission) {
	if admission != earlyFlakeDetectionAdmissionAllowed && g.terminal.CompareAndSwap(uint32(earlyFlakeDetectionAdmissionAllowed), uint32(admission)) {
		log.Debug("civisibility: EFD faulty-session admission became terminal: %s", efdFaultySessionAdmissionName(admission))
	}
}

func efdFaultySessionAdmissionName(admission earlyFlakeDetectionAdmission) string {
	switch admission {
	case earlyFlakeDetectionAdmissionFaulty:
		return "faulty"
	case earlyFlakeDetectionAdmissionUnavailable:
		return "unavailable"
	default:
		return "allowed"
	}
}

func countUniqueKnownTopLevelTests(knownTests *civisibilitynet.KnownTestsResponseData) uint64 {
	if knownTests == nil {
		return 0
	}
	unique := make(map[efdFaultySessionIdentity]struct{})
	for module, suites := range knownTests.Tests {
		for suite, tests := range suites {
			for _, test := range tests {
				name, topLevel := topLevelTestName(test)
				if !topLevel || name == "" {
					continue
				}
				unique[efdFaultySessionIdentity{module: module, suite: suite, test: name}] = struct{}{}
			}
		}
	}
	return uint64(len(unique))
}

func efdFaultySessionLimit(threshold, known uint64) (uint64, bool) {
	if threshold > 99 {
		return 0, false
	}
	if threshold == 0 {
		return 0, true
	}
	denominator := uint64(100) - threshold
	high, low := bits.Mul64(threshold, known)
	if high >= denominator {
		return 0, false
	}
	quotient, _ := bits.Div64(high, low, denominator)
	limit := max(threshold, quotient)
	if limit > math.MaxInt64 {
		return 0, false
	}
	return limit, true
}

func efdFaultySessionInvocationRoot() (string, bool) {
	executable, err := os.Executable()
	if err != nil {
		return "", false
	}
	if resolved, resolveErr := filepath.EvalSymlinks(executable); resolveErr == nil {
		executable = resolved
	}
	return efdFaultySessionInvocationRootFromExecutable(executable)
}

func efdFaultySessionInvocationRootFromExecutable(executable string) (string, bool) {
	for directory := filepath.Dir(executable); ; directory = filepath.Dir(directory) {
		if isGoBuildDirectory(filepath.Base(directory)) {
			relative, err := filepath.Rel(directory, executable)
			if err == nil {
				component := relative
				if separator := strings.IndexRune(relative, filepath.Separator); separator >= 0 {
					component = relative[:separator]
				}
				if isGoBuildBinaryDirectory(component) {
					return directory, true
				}
			}
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			return "", false
		}
	}
}

func isGoBuildDirectory(name string) bool {
	return hasNumericSuffix(name, "go-build")
}

func isGoBuildBinaryDirectory(name string) bool {
	return hasNumericSuffix(name, "b")
}

func hasNumericSuffix(value, prefix string) bool {
	if !strings.HasPrefix(value, prefix) || len(value) == len(prefix) {
		return false
	}
	for _, char := range value[len(prefix):] {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

func (s *efdFaultySessionLocalStore) claim() earlyFlakeDetectionAdmission {
	s.mu.Lock()
	defer s.mu.Unlock()
	if terminal := earlyFlakeDetectionAdmission(s.terminal.Load()); terminal != earlyFlakeDetectionAdmissionAllowed {
		return terminal
	}
	if s.threshold == 0 {
		s.terminal.CompareAndSwap(0, uint32(earlyFlakeDetectionAdmissionFaulty))
		return earlyFlakeDetectionAdmissionFaulty
	}
	s.initOnce.Do(func() {
		limit, ok := efdFaultySessionLimit(s.threshold, s.knownCount())
		if !ok {
			s.terminal.Store(uint32(earlyFlakeDetectionAdmissionUnavailable))
			return
		}
		s.limit = limit
	})
	if terminal := s.retryStateOnly(); terminal != earlyFlakeDetectionAdmissionAllowed {
		return terminal
	}
	if s.count >= s.limit {
		s.terminal.CompareAndSwap(0, uint32(earlyFlakeDetectionAdmissionFaulty))
		return earlyFlakeDetectionAdmissionFaulty
	}
	s.count++
	return earlyFlakeDetectionAdmissionAllowed
}

func (s *efdFaultySessionLocalStore) observe() (earlyFlakeDetectionAdmission, bool) {
	admission := s.retryStateOnly()
	return admission, admission != earlyFlakeDetectionAdmissionAllowed
}

func (s *efdFaultySessionLocalStore) retryStateOnly() earlyFlakeDetectionAdmission {
	return earlyFlakeDetectionAdmission(s.terminal.Load())
}

func (s *efdFaultySessionFilesystemStore) claim() earlyFlakeDetectionAdmission {
	if s.threshold == 0 {
		if err := s.ensureDirectory(); err != nil {
			return earlyFlakeDetectionAdmissionUnavailable
		}
		_ = s.writeTerminal("faulty")
		return earlyFlakeDetectionAdmissionFaulty
	}
	if admission, recognized := s.observe(); recognized || admission != earlyFlakeDetectionAdmissionAllowed {
		return admission
	}
	lock, err := s.acquireLock()
	if err != nil {
		if admission, recognized := s.observe(); recognized {
			return admission
		}
		_ = s.writeTerminal("unavailable")
		return earlyFlakeDetectionAdmissionUnavailable
	}
	admission := s.claimLocked()
	if releaseErr := s.releaseLock(lock); releaseErr != nil && admission == earlyFlakeDetectionAdmissionAllowed {
		_ = s.writeTerminal("unavailable")
		return earlyFlakeDetectionAdmissionUnavailable
	}
	return admission
}

func (s *efdFaultySessionFilesystemStore) observe() (earlyFlakeDetectionAdmission, bool) {
	path := filepath.Join(s.directory, efdFaultySessionTerminalFile)
	contents, err := readEFDPrivateFile(path, int64(len("unavailable")))
	if errors.Is(err, os.ErrNotExist) {
		return earlyFlakeDetectionAdmissionAllowed, false
	}
	if err != nil {
		return earlyFlakeDetectionAdmissionUnavailable, false
	}
	switch string(contents) {
	case "faulty":
		return earlyFlakeDetectionAdmissionFaulty, true
	case "unavailable":
		return earlyFlakeDetectionAdmissionUnavailable, true
	default:
		return earlyFlakeDetectionAdmissionUnavailable, false
	}
}

func (s *efdFaultySessionFilesystemStore) claimLocked() earlyFlakeDetectionAdmission {
	if admission, recognized := s.observe(); recognized || admission != earlyFlakeDetectionAdmissionAllowed {
		return admission
	}
	state, err := s.loadOrInitializeState()
	if err != nil {
		_ = s.writeTerminal("unavailable")
		return earlyFlakeDetectionAdmissionUnavailable
	}
	countPath := filepath.Join(s.directory, efdFaultySessionCountFile)
	pathInfo, err := os.Lstat(countPath)
	if err != nil || validateEFDPrivateFile(pathInfo) != nil {
		_ = s.writeTerminal("unavailable")
		return earlyFlakeDetectionAdmissionUnavailable
	}
	countFile, err := os.OpenFile(countPath, os.O_RDWR, 0)
	if err != nil {
		_ = s.writeTerminal("unavailable")
		return earlyFlakeDetectionAdmissionUnavailable
	}
	info, statErr := countFile.Stat()
	if statErr != nil || validateEFDPrivateFile(info) != nil || !os.SameFile(pathInfo, info) || info.Size() < 0 || uint64(info.Size()) > state.Limit {
		_ = countFile.Close()
		_ = s.writeTerminal("unavailable")
		return earlyFlakeDetectionAdmissionUnavailable
	}
	if uint64(info.Size()) == state.Limit {
		_ = countFile.Close()
		_ = s.writeTerminal("faulty")
		return earlyFlakeDetectionAdmissionFaulty
	}
	if err := countFile.Truncate(info.Size() + 1); err != nil {
		_ = countFile.Close()
		_ = s.writeTerminal("unavailable")
		return earlyFlakeDetectionAdmissionUnavailable
	}
	if err := countFile.Close(); err != nil {
		_ = s.writeTerminal("unavailable")
		return earlyFlakeDetectionAdmissionUnavailable
	}
	if admission, recognized := s.observe(); recognized || admission != earlyFlakeDetectionAdmissionAllowed {
		return admission
	}
	return earlyFlakeDetectionAdmissionAllowed
}

func (s *efdFaultySessionFilesystemStore) loadOrInitializeState() (efdFaultySessionState, error) {
	statePath := filepath.Join(s.directory, efdFaultySessionStateFile)
	state, err := readEFDState(statePath)
	if err == nil {
		if state.Threshold != s.threshold {
			return efdFaultySessionState{}, errors.New("faulty-session threshold mismatch")
		}
		limit, ok := efdFaultySessionLimit(state.Threshold, state.Known)
		if !ok || limit != state.Limit {
			return efdFaultySessionState{}, errors.New("faulty-session state limit mismatch")
		}
		return state, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return efdFaultySessionState{}, err
	}
	if _, countErr := os.Lstat(filepath.Join(s.directory, efdFaultySessionCountFile)); !errors.Is(countErr, os.ErrNotExist) {
		return efdFaultySessionState{}, errors.New("orphaned faulty-session count")
	}
	known := s.knownCount()
	if admission, recognized := s.observe(); recognized || admission != earlyFlakeDetectionAdmissionAllowed {
		return efdFaultySessionState{}, errors.New("faulty-session terminal observed during initialization")
	}
	limit, ok := efdFaultySessionLimit(s.threshold, known)
	if !ok {
		return efdFaultySessionState{}, errors.New("faulty-session limit overflow")
	}
	state = efdFaultySessionState{Version: efdFaultySessionSchemaVersion, Threshold: s.threshold, Known: known, Limit: limit}
	countFile, err := os.OpenFile(filepath.Join(s.directory, efdFaultySessionCountFile), os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return efdFaultySessionState{}, err
	}
	if err := countFile.Close(); err != nil {
		return efdFaultySessionState{}, err
	}
	if err := s.writeState(statePath, state); err != nil {
		return efdFaultySessionState{}, err
	}
	return state, nil
}

func (s *efdFaultySessionFilesystemStore) ensureDirectory() error {
	if err := os.MkdirAll(s.directory, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(s.directory)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("faulty-session path is not a private directory")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return errors.New("faulty-session directory permissions are too broad")
	}
	return nil
}

func (s *efdFaultySessionFilesystemStore) acquireLock() (*os.File, error) {
	if err := s.ensureDirectory(); err != nil {
		return nil, err
	}
	deadline := s.now().Add(efdFaultySessionLockTimeout)
	backoff := efdFaultySessionInitialBackoff
	lockPath := filepath.Join(s.directory, efdFaultySessionLockFile)
	for {
		lock, err := os.OpenFile(lockPath, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			return lock, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		if admission, recognized := s.observe(); recognized || admission != earlyFlakeDetectionAdmissionAllowed {
			return nil, errors.New("faulty-session terminal observed while waiting for lock")
		}
		if !s.now().Before(deadline) {
			return nil, errors.New("faulty-session lock timeout")
		}
		s.sleep(backoff)
		backoff = min(backoff*2, efdFaultySessionMaxBackoff)
	}
}

func (s *efdFaultySessionFilesystemStore) releaseLock(lock *os.File) error {
	if lock == nil {
		return errors.New("nil faulty-session lock")
	}
	owner, err := lock.Stat()
	if err != nil {
		_ = lock.Close()
		return err
	}
	if err := lock.Close(); err != nil {
		return err
	}
	lockPath := filepath.Join(s.directory, efdFaultySessionLockFile)
	current, err := os.Lstat(lockPath)
	if err != nil {
		return err
	}
	if !os.SameFile(owner, current) {
		return errors.New("faulty-session lock ownership changed")
	}
	return os.Remove(lockPath)
}

func (s *efdFaultySessionFilesystemStore) publishTerminal(value string) error {
	if err := s.ensureDirectory(); err != nil {
		return err
	}
	path := filepath.Join(s.directory, efdFaultySessionTerminalFile)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err := io.WriteString(file, value); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func (s *efdFaultySessionFilesystemStore) writeTerminal(value string) error {
	if s.terminalWriter != nil {
		return s.terminalWriter(value)
	}
	return s.publishTerminal(value)
}

func (s *efdFaultySessionFilesystemStore) writeState(path string, state efdFaultySessionState) error {
	if s.stateWriter != nil {
		return s.stateWriter(path, state)
	}
	return writeEFDState(path, state)
}

func readEFDState(path string) (efdFaultySessionState, error) {
	contents, err := readEFDPrivateFile(path, efdFaultySessionMaxStateBytes)
	if err != nil {
		return efdFaultySessionState{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	var state efdFaultySessionState
	if err := decoder.Decode(&state); err != nil {
		return efdFaultySessionState{}, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return efdFaultySessionState{}, errors.New("faulty-session state has trailing data")
	}
	if state.Version != efdFaultySessionSchemaVersion || state.Threshold == 0 || state.Threshold > 99 || state.Limit > math.MaxInt64 {
		return efdFaultySessionState{}, errors.New("faulty-session state is invalid")
	}
	return state, nil
}

func readEFDPrivateFile(path string, maxBytes int64) ([]byte, error) {
	pathInfo, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if err := validateEFDPrivateFile(pathInfo); err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if err := validateEFDPrivateFile(info); err != nil {
		return nil, err
	}
	if !os.SameFile(pathInfo, info) {
		return nil, errors.New("faulty-session file ownership changed")
	}
	contents, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(contents)) > maxBytes {
		return nil, errors.New("faulty-session file is oversized")
	}
	return contents, nil
}

func writeEFDState(path string, state efdFaultySessionState) error {
	contents, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if len(contents) > efdFaultySessionMaxStateBytes {
		return errors.New("faulty-session state is oversized")
	}
	directory := filepath.Dir(path)
	file, err := os.CreateTemp(directory, ".efd-state-")
	if err != nil {
		return err
	}
	temporary := file.Name()
	removeTemporary := true
	defer func() {
		if removeTemporary {
			_ = os.Remove(temporary)
		}
	}()
	if _, err := file.Write(contents); err != nil {
		_ = file.Close()
		return err
	}
	owner, err := file.Stat()
	if err != nil || validateEFDPrivateFile(owner) != nil {
		_ = file.Close()
		if err != nil {
			return err
		}
		return errors.New("faulty-session state temporary file is invalid")
	}
	if err := file.Close(); err != nil {
		return err
	}
	current, err := os.Lstat(temporary)
	if err != nil || validateEFDPrivateFile(current) != nil || !os.SameFile(owner, current) {
		if err != nil {
			return err
		}
		return errors.New("faulty-session state temporary file ownership changed")
	}
	if err := os.Rename(temporary, path); err != nil {
		return err
	}
	removeTemporary = false
	return nil
}

func validateEFDPrivateFile(info os.FileInfo) error {
	if info == nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return errors.New("faulty-session file is not regular")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("faulty-session file permissions are too broad: %s", info.Mode().Perm())
	}
	return nil
}

func markEFDSessionFaultyIfNeeded(guard earlyFlakeDetectionFaultySession) {
	if guard == nil || session == nil || guard.retryState() != earlyFlakeDetectionAdmissionFaulty {
		return
	}
	session.SetTag(constants.TestEarlyFlakeDetectionRetryAborted, "faulty")
}
