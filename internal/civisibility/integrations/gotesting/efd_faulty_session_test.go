// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gotesting

import (
	"context"
	"errors"
	"math"
	"math/big"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations"
	civisibilitynet "github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils/net"
)

func TestProcessRetryEFDFaultySessionLimit(t *testing.T) {
	tests := []struct {
		name      string
		threshold uint64
		known     uint64
		want      uint64
		ok        bool
	}{
		{name: "zero", threshold: 0, known: 100, want: 0, ok: true},
		{name: "absolute threshold dominates", threshold: 10, known: 1, want: 10, ok: true},
		{name: "percentage boundary", threshold: 10, known: 90, want: 10, ok: true},
		{name: "percentage dominates", threshold: 30, known: 700, want: 300, ok: true},
		{name: "high multiplication word is supported", threshold: 2, known: math.MaxUint64, want: math.MaxUint64 / 49, ok: true},
		{name: "unrepresentable quotient is rejected", threshold: 99, known: math.MaxUint64, ok: false},
		{name: "hundred disabled", threshold: 100, known: 1, ok: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := efdFaultySessionLimit(tt.threshold, tt.known)
			require.Equal(t, tt.ok, ok)
			if ok {
				require.Equal(t, tt.want, got)
			}
		})
	}
}

func TestProcessRetryEFDFaultySessionLimitMatchesFaultyPredicate(t *testing.T) {
	for threshold := range uint64(100) {
		for known := uint64(0); known <= 1_000; known++ {
			limit, ok := efdFaultySessionLimit(threshold, known)
			require.True(t, ok)

			product := new(big.Int).Mul(new(big.Int).SetUint64(threshold), new(big.Int).SetUint64(known))
			quotient := new(big.Int).Div(product, new(big.Int).SetUint64(100-threshold))
			reference := max(threshold, quotient.Uint64())
			require.Equal(t, reference, limit)

			for _, newTests := range []uint64{0, threshold, threshold + 1, limit, limit + 1} {
				percentageLeft := new(big.Int).Mul(big.NewInt(100), new(big.Int).SetUint64(newTests))
				population := new(big.Int).Add(new(big.Int).SetUint64(known), new(big.Int).SetUint64(newTests))
				percentageRight := new(big.Int).Mul(new(big.Int).SetUint64(threshold), population)
				faulty := newTests > threshold && percentageLeft.Cmp(percentageRight) > 0
				require.Equal(t, newTests > limit, faulty, "threshold=%d known=%d new=%d limit=%d", threshold, known, newTests, limit)
			}
		}
	}
}

func TestProcessRetryTopLevelTestNameMatchesIdentityBoundary(t *testing.T) {
	for _, name := range []string{"", "TestTopLevel", "TestTopLevel/subtest", "/leading", "trailing/", "a//b"} {
		topLevelName, topLevel := topLevelTestName(name)
		identity := newTestIdentity("module", "suite", name)
		require.Equal(t, len(identity.Segments) == 1, topLevel, name)
		if name != "" {
			require.Equal(t, identity.BaseName, topLevelName, name)
		}
	}
}

func TestProcessRetryCountUniqueKnownTopLevelTests(t *testing.T) {
	known := &civisibilitynet.KnownTestsResponseData{Tests: civisibilitynet.KnownTestsResponseDataModules{
		"module": {
			"suite":  {"TestOne", "TestOne", "TestOne/subtest", "TestTwo"},
			"suite2": {"TestOne"},
		},
		"module2": {"suite": {"TestOne"}},
	}}
	require.Equal(t, uint64(4), countUniqueKnownTopLevelTests(known))
}

func TestProcessRetryEFDFaultySessionLocalStoreConcurrentBoundary(t *testing.T) {
	var knownCalls atomic.Int32
	store := &efdFaultySessionLocalStore{
		threshold: 10,
		knownCount: func() uint64 {
			knownCalls.Add(1)
			return 90
		},
	}

	const claimers = 100
	start := make(chan struct{})
	results := make(chan earlyFlakeDetectionAdmission, claimers)
	var wait sync.WaitGroup
	wait.Add(claimers)
	for range claimers {
		go func() {
			defer wait.Done()
			<-start
			results <- store.claim()
		}()
	}
	close(start)
	wait.Wait()
	close(results)

	allowed := 0
	for result := range results {
		if result == earlyFlakeDetectionAdmissionAllowed {
			allowed++
		}
	}
	require.Equal(t, 10, allowed)
	require.Equal(t, int32(1), knownCalls.Load())
	require.Equal(t, earlyFlakeDetectionAdmissionFaulty, store.retryStateOnly())
}

func TestProcessRetryEFDFaultySessionZeroThresholdSkipsKnownCount(t *testing.T) {
	var knownCalls atomic.Int32
	store := &efdFaultySessionLocalStore{
		threshold: 0,
		knownCount: func() uint64 {
			knownCalls.Add(1)
			return 100
		},
	}
	require.Equal(t, earlyFlakeDetectionAdmissionFaulty, store.claim())
	require.Zero(t, knownCalls.Load())
}

type blockingEFDFaultySessionStore struct {
	entered chan struct{}
	release chan struct{}
	calls   atomic.Int32
}

type recordingEFDFaultySessionGuard struct {
	mu             sync.Mutex
	admissions     []earlyFlakeDetectionAdmission
	faultyObserved chan struct{}
	faultyOnce     sync.Once
	admitCalls     int
	retryCalls     int
}

func (g *recordingEFDFaultySessionGuard) nextAdmission() earlyFlakeDetectionAdmission {
	if len(g.admissions) == 0 {
		return earlyFlakeDetectionAdmissionAllowed
	}
	admission := g.admissions[0]
	g.admissions = g.admissions[1:]
	if admission != earlyFlakeDetectionAdmissionAllowed && g.faultyObserved != nil {
		g.faultyOnce.Do(func() { close(g.faultyObserved) })
	}
	return admission
}

func (g *recordingEFDFaultySessionGuard) admitNewTest(*testIdentity) earlyFlakeDetectionAdmission {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.admitCalls++
	return g.nextAdmission()
}

func (g *recordingEFDFaultySessionGuard) retryState() earlyFlakeDetectionAdmission {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.retryCalls++
	return g.nextAdmission()
}

func (s *blockingEFDFaultySessionStore) claim() earlyFlakeDetectionAdmission {
	if s.calls.Add(1) == 1 {
		close(s.entered)
	}
	<-s.release
	return earlyFlakeDetectionAdmissionAllowed
}

func (*blockingEFDFaultySessionStore) observe() (earlyFlakeDetectionAdmission, bool) {
	return earlyFlakeDetectionAdmissionAllowed, false
}

func TestProcessRetryEFDFaultySessionGuardDeduplicatesConcurrentIdentity(t *testing.T) {
	store := &blockingEFDFaultySessionStore{entered: make(chan struct{}), release: make(chan struct{})}
	guard := &efdFaultySessionGuard{
		identities: make(map[efdFaultySessionIdentity]*efdFaultySessionIdentityEntry),
		store:      store,
	}
	identity := newTestIdentity("module", "suite", "TestName")
	results := make(chan earlyFlakeDetectionAdmission, 2)
	go func() { results <- guard.admitNewTest(identity) }()
	<-store.entered
	go func() { results <- guard.admitNewTest(identity) }()
	close(store.release)
	require.Equal(t, earlyFlakeDetectionAdmissionAllowed, <-results)
	require.Equal(t, earlyFlakeDetectionAdmissionAllowed, <-results)
	require.Equal(t, int32(1), store.calls.Load())
}

func TestProcessRetryEFDFaultySessionGuardClaimsDifferentIdentities(t *testing.T) {
	store := &countingEFDFaultySessionStore{}
	guard := &efdFaultySessionGuard{
		identities: make(map[efdFaultySessionIdentity]*efdFaultySessionIdentityEntry),
		store:      store,
	}
	require.Equal(t, earlyFlakeDetectionAdmissionAllowed, guard.admitNewTest(newTestIdentity("module", "suite", "TestOne")))
	require.Equal(t, earlyFlakeDetectionAdmissionAllowed, guard.admitNewTest(newTestIdentity("module", "suite", "TestTwo")))
	require.Equal(t, int32(2), store.calls.Load())
}

type countingEFDFaultySessionStore struct {
	calls atomic.Int32
}

func (s *countingEFDFaultySessionStore) claim() earlyFlakeDetectionAdmission {
	s.calls.Add(1)
	return earlyFlakeDetectionAdmissionAllowed
}

func (*countingEFDFaultySessionStore) observe() (earlyFlakeDetectionAdmission, bool) {
	return earlyFlakeDetectionAdmissionAllowed, false
}

func TestProcessRetryEFDFaultySessionFilesystemStoreExactBoundary(t *testing.T) {
	directory := filepath.Join(t.TempDir(), efdFaultySessionDirectoryName)
	store := &efdFaultySessionFilesystemStore{
		directory:  directory,
		threshold:  10,
		knownCount: func() uint64 { return 90 },
		now:        time.Now,
		sleep:      time.Sleep,
	}
	for range 10 {
		require.Equal(t, earlyFlakeDetectionAdmissionAllowed, store.claim())
	}
	require.Equal(t, earlyFlakeDetectionAdmissionFaulty, store.claim())
	info, err := os.Stat(filepath.Join(directory, efdFaultySessionCountFile))
	require.NoError(t, err)
	require.Equal(t, int64(10), info.Size())
	contents, err := os.ReadFile(filepath.Join(directory, efdFaultySessionTerminalFile))
	require.NoError(t, err)
	require.Equal(t, "faulty", string(contents))
}

func TestProcessRetryEFDFaultySessionFilesystemStoresShareState(t *testing.T) {
	directory := filepath.Join(t.TempDir(), efdFaultySessionDirectoryName)
	newStore := func() *efdFaultySessionFilesystemStore {
		return &efdFaultySessionFilesystemStore{
			directory:  directory,
			threshold:  1,
			knownCount: func() uint64 { return 1 },
			now:        time.Now,
			sleep:      time.Sleep,
		}
	}
	require.Equal(t, earlyFlakeDetectionAdmissionAllowed, newStore().claim())
	require.Equal(t, earlyFlakeDetectionAdmissionFaulty, newStore().claim())
	admission, recognized := newStore().observe()
	require.True(t, recognized)
	require.Equal(t, earlyFlakeDetectionAdmissionFaulty, admission)
}

func TestProcessRetryEFDFaultySessionFilesystemStoreConcurrentBoundary(t *testing.T) {
	directory := filepath.Join(t.TempDir(), efdFaultySessionDirectoryName)
	const claimers = 100
	start := make(chan struct{})
	results := make(chan earlyFlakeDetectionAdmission, claimers)
	var wait sync.WaitGroup
	wait.Add(claimers)
	for range claimers {
		go func() {
			defer wait.Done()
			<-start
			store := &efdFaultySessionFilesystemStore{
				directory:  directory,
				threshold:  10,
				knownCount: func() uint64 { return 90 },
				now:        func() time.Time { return time.Unix(0, 0) },
				sleep:      func(time.Duration) { runtime.Gosched() },
			}
			results <- store.claim()
		}()
	}
	close(start)
	wait.Wait()
	close(results)

	allowed := 0
	for admission := range results {
		if admission == earlyFlakeDetectionAdmissionAllowed {
			allowed++
		}
	}
	info, err := os.Stat(filepath.Join(directory, efdFaultySessionCountFile))
	require.NoError(t, err)
	require.Equal(t, int64(10), info.Size())
	require.Positive(t, allowed)
	require.LessOrEqual(t, int64(allowed), info.Size())
}

func TestProcessRetryEFDFaultySessionZeroThresholdConcurrentPublishers(t *testing.T) {
	directory := filepath.Join(t.TempDir(), efdFaultySessionDirectoryName)
	const claimers = 100
	start := make(chan struct{})
	results := make(chan earlyFlakeDetectionAdmission, claimers)
	var knownCalls atomic.Int32
	var wait sync.WaitGroup
	wait.Add(claimers)
	for range claimers {
		go func() {
			defer wait.Done()
			<-start
			store := &efdFaultySessionFilesystemStore{
				directory: directory,
				threshold: 0,
				knownCount: func() uint64 {
					knownCalls.Add(1)
					return 1
				},
			}
			results <- store.claim()
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	for admission := range results {
		require.Equal(t, earlyFlakeDetectionAdmissionFaulty, admission)
	}
	require.Zero(t, knownCalls.Load())
	contents, err := os.ReadFile(filepath.Join(directory, efdFaultySessionTerminalFile))
	require.NoError(t, err)
	require.Equal(t, "faulty", string(contents))
	_, err = os.Stat(filepath.Join(directory, efdFaultySessionStateFile))
	require.ErrorIs(t, err, os.ErrNotExist)
	_, err = os.Stat(filepath.Join(directory, efdFaultySessionCountFile))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestProcessRetryEFDFaultySessionLockReleasePreservesOwnership(t *testing.T) {
	directory := filepath.Join(t.TempDir(), efdFaultySessionDirectoryName)
	store := &efdFaultySessionFilesystemStore{
		directory: directory,
		now:       time.Now,
		sleep:     time.Sleep,
	}
	lock, err := store.acquireLock()
	require.NoError(t, err)
	lockPath := filepath.Join(directory, efdFaultySessionLockFile)
	if runtime.GOOS == "windows" {
		// Windows prevents replacing a lock while its owning handle is open.
		require.Error(t, os.Remove(lockPath))
		require.NoError(t, store.releaseLock(lock))
		_, err = os.Stat(lockPath)
		require.ErrorIs(t, err, os.ErrNotExist)
		return
	}
	require.NoError(t, os.Remove(lockPath))
	require.NoError(t, os.WriteFile(lockPath, []byte("replacement"), 0o600))
	require.Error(t, store.releaseLock(lock))
	contents, err := os.ReadFile(lockPath)
	require.NoError(t, err)
	require.Equal(t, "replacement", string(contents))
}

func TestProcessRetryReadEFDFaultySessionStateStrictValidation(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, efdFaultySessionStateFile)
	tests := []string{
		`{"version":1,"threshold":10,"known":90,"limit":10,"unknown":true}`,
		`{"version":1,"threshold":10,"known":90,"limit":10}{}`,
		`{"version":2,"threshold":10,"known":90,"limit":10}`,
		`{"version":1,"threshold":0,"known":90,"limit":0}`,
		`{"version":1,"threshold":100,"known":90,"limit":0}`,
		`{"version":1,"threshold":-1,"known":90,"limit":0}`,
		`{"version":1,"threshold":10,"known":-1,"limit":10}`,
	}
	for _, contents := range tests {
		require.NoError(t, os.WriteFile(path, []byte(contents), 0o600))
		_, err := readEFDState(path)
		require.Error(t, err)
	}
	require.NoError(t, os.WriteFile(path, make([]byte, efdFaultySessionMaxStateBytes+1), 0o600))
	_, err := readEFDState(path)
	require.Error(t, err)
}

func TestProcessRetryEFDFaultySessionFilesystemCorruptionNeverAdmits(t *testing.T) {
	newStore := func(directory string) *efdFaultySessionFilesystemStore {
		return &efdFaultySessionFilesystemStore{
			directory:  directory,
			threshold:  10,
			knownCount: func() uint64 { return 90 },
			now:        time.Now,
			sleep:      time.Sleep,
		}
	}
	writeState := func(t *testing.T, directory string, state efdFaultySessionState) {
		t.Helper()
		require.NoError(t, os.MkdirAll(directory, 0o700))
		require.NoError(t, writeEFDState(filepath.Join(directory, efdFaultySessionStateFile), state))
	}

	tests := []struct {
		name  string
		setup func(*testing.T, string)
	}{
		{
			name: "orphaned count",
			setup: func(t *testing.T, directory string) {
				require.NoError(t, os.MkdirAll(directory, 0o700))
				require.NoError(t, os.WriteFile(filepath.Join(directory, efdFaultySessionCountFile), nil, 0o600))
			},
		},
		{
			name: "orphaned state",
			setup: func(t *testing.T, directory string) {
				writeState(t, directory, efdFaultySessionState{Version: 1, Threshold: 10, Known: 90, Limit: 10})
			},
		},
		{
			name: "threshold mismatch",
			setup: func(t *testing.T, directory string) {
				writeState(t, directory, efdFaultySessionState{Version: 1, Threshold: 20, Known: 80, Limit: 20})
				require.NoError(t, os.WriteFile(filepath.Join(directory, efdFaultySessionCountFile), nil, 0o600))
			},
		},
		{
			name: "limit mismatch",
			setup: func(t *testing.T, directory string) {
				writeState(t, directory, efdFaultySessionState{Version: 1, Threshold: 10, Known: 90, Limit: 9})
				require.NoError(t, os.WriteFile(filepath.Join(directory, efdFaultySessionCountFile), nil, 0o600))
			},
		},
		{
			name: "counter exceeds limit",
			setup: func(t *testing.T, directory string) {
				writeState(t, directory, efdFaultySessionState{Version: 1, Threshold: 10, Known: 90, Limit: 10})
				require.NoError(t, os.WriteFile(filepath.Join(directory, efdFaultySessionCountFile), make([]byte, 11), 0o600))
			},
		},
		{
			name: "counter is directory",
			setup: func(t *testing.T, directory string) {
				writeState(t, directory, efdFaultySessionState{Version: 1, Threshold: 10, Known: 90, Limit: 10})
				require.NoError(t, os.Mkdir(filepath.Join(directory, efdFaultySessionCountFile), 0o700))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			directory := filepath.Join(t.TempDir(), efdFaultySessionDirectoryName)
			tt.setup(t, directory)
			require.Equal(t, earlyFlakeDetectionAdmissionUnavailable, newStore(directory).claim())
		})
	}
}

func TestProcessRetryEFDFaultySessionFilesystemFailuresNeverAdmit(t *testing.T) {
	directory := filepath.Join(t.TempDir(), efdFaultySessionDirectoryName)
	store := &efdFaultySessionFilesystemStore{
		directory:  directory,
		threshold:  10,
		knownCount: func() uint64 { return 90 },
		now:        time.Now,
		sleep:      time.Sleep,
		stateWriter: func(string, efdFaultySessionState) error {
			return errors.New("injected state write failure")
		},
	}
	require.Equal(t, earlyFlakeDetectionAdmissionUnavailable, store.claim())
	admission, recognized := store.observe()
	require.True(t, recognized)
	require.Equal(t, earlyFlakeDetectionAdmissionUnavailable, admission)
}

func TestProcessRetryEFDFaultySessionCrossingRemainsLocallyFaultyWhenPublicationFails(t *testing.T) {
	for _, test := range []struct {
		name      string
		threshold uint64
		known     uint64
		claims    int
	}{
		{name: "zero threshold", threshold: 0, claims: 1},
		{name: "counter boundary", threshold: 1, known: 1, claims: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := filepath.Join(t.TempDir(), efdFaultySessionDirectoryName)
			store := &efdFaultySessionFilesystemStore{
				directory:  directory,
				threshold:  test.threshold,
				knownCount: func() uint64 { return test.known },
				now:        time.Now,
				sleep:      time.Sleep,
				terminalWriter: func(string) error {
					return errors.New("injected terminal write failure")
				},
			}
			guard := &efdFaultySessionGuard{
				identities: make(map[efdFaultySessionIdentity]*efdFaultySessionIdentityEntry),
				store:      store,
			}
			for index := 0; index < test.claims-1; index++ {
				require.Equal(t, earlyFlakeDetectionAdmissionAllowed, guard.admitNewTest(newTestIdentity("module", "suite", "TestAllowed"+strconv.Itoa(index))))
			}
			require.Equal(t, earlyFlakeDetectionAdmissionFaulty, guard.admitNewTest(newTestIdentity("module", "suite", "TestCrossing")))
			require.Equal(t, earlyFlakeDetectionAdmissionFaulty, guard.retryState())
			_, err := os.Stat(filepath.Join(directory, efdFaultySessionTerminalFile))
			require.ErrorIs(t, err, os.ErrNotExist)

			recorder, restoreSession := setProcessRetryRecordingSessionForTesting(t)
			defer restoreSession()
			markEFDSessionFaultyIfNeeded(guard)
			value, tagged := recorder.GetTag(constants.TestEarlyFlakeDetectionRetryAborted)
			require.True(t, tagged)
			require.Equal(t, "faulty", value)
		})
	}
}

func TestProcessRetryEFDFaultySessionRejectsNonPrivatePaths(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permission bits are not enforced on Windows")
	}
	t.Run("directory permissions", func(t *testing.T) {
		directory := filepath.Join(t.TempDir(), efdFaultySessionDirectoryName)
		require.NoError(t, os.Mkdir(directory, 0o755))
		store := &efdFaultySessionFilesystemStore{directory: directory, threshold: 10, knownCount: func() uint64 { return 90 }, now: time.Now, sleep: time.Sleep}
		require.Equal(t, earlyFlakeDetectionAdmissionUnavailable, store.claim())
	})
	t.Run("state permissions", func(t *testing.T) {
		directory := filepath.Join(t.TempDir(), efdFaultySessionDirectoryName)
		require.NoError(t, os.Mkdir(directory, 0o700))
		statePath := filepath.Join(directory, efdFaultySessionStateFile)
		require.NoError(t, os.WriteFile(statePath, []byte(`{"version":1,"threshold":10,"known":90,"limit":10}`), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(directory, efdFaultySessionCountFile), nil, 0o600))
		store := &efdFaultySessionFilesystemStore{directory: directory, threshold: 10, knownCount: func() uint64 { return 90 }, now: time.Now, sleep: time.Sleep}
		require.Equal(t, earlyFlakeDetectionAdmissionUnavailable, store.claim())
	})
}

func TestProcessRetryEFDFaultySessionRejectsMalformedTerminalWithoutCachingIt(t *testing.T) {
	directory := filepath.Join(t.TempDir(), efdFaultySessionDirectoryName)
	require.NoError(t, os.MkdirAll(directory, 0o700))
	terminalPath := filepath.Join(directory, efdFaultySessionTerminalFile)
	store := &efdFaultySessionFilesystemStore{directory: directory}

	for _, contents := range [][]byte{[]byte("unknown"), make([]byte, len("unavailable")+1)} {
		require.NoError(t, os.WriteFile(terminalPath, contents, 0o600))
		admission, recognized := store.observe()
		require.False(t, recognized)
		require.Equal(t, earlyFlakeDetectionAdmissionUnavailable, admission)
	}
	require.NoError(t, os.WriteFile(terminalPath, []byte("faulty"), 0o600))
	admission, recognized := store.observe()
	require.True(t, recognized)
	require.Equal(t, earlyFlakeDetectionAdmissionFaulty, admission)
}

func TestProcessRetryEFDFaultySessionPartialTerminalDoesNotTagFinalizer(t *testing.T) {
	directory := filepath.Join(t.TempDir(), efdFaultySessionDirectoryName)
	require.NoError(t, os.MkdirAll(directory, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(directory, efdFaultySessionTerminalFile), nil, 0o600))
	guard := &efdFaultySessionGuard{
		identities: make(map[efdFaultySessionIdentity]*efdFaultySessionIdentityEntry),
		store:      &efdFaultySessionFilesystemStore{directory: directory},
	}
	recorder, restoreSession := setProcessRetryRecordingSessionForTesting(t)
	defer restoreSession()
	markEFDSessionFaultyIfNeeded(guard)
	_, tagged := recorder.GetTag(constants.TestEarlyFlakeDetectionRetryAborted)
	require.False(t, tagged)
}

func TestProcessRetryEFDFaultySessionStateInitializationScansKnownTestsOnce(t *testing.T) {
	directory := filepath.Join(t.TempDir(), efdFaultySessionDirectoryName)
	var knownCalls atomic.Int32
	newStore := func() *efdFaultySessionFilesystemStore {
		return &efdFaultySessionFilesystemStore{
			directory: directory,
			threshold: 10,
			knownCount: func() uint64 {
				knownCalls.Add(1)
				return 90
			},
			now:   time.Now,
			sleep: time.Sleep,
		}
	}
	require.Equal(t, earlyFlakeDetectionAdmissionAllowed, newStore().claim())
	require.Equal(t, earlyFlakeDetectionAdmissionAllowed, newStore().claim())
	require.Equal(t, int32(1), knownCalls.Load())
}

func TestProcessRetryEFDFaultySessionInvocationRootFromExecutable(t *testing.T) {
	root := filepath.Join(t.TempDir(), "go-build123")
	executable := filepath.Join(root, "b001", "package.test")
	got, ok := efdFaultySessionInvocationRootFromExecutable(executable)
	require.True(t, ok)
	require.Equal(t, root, got)

	_, ok = efdFaultySessionInvocationRootFromExecutable(filepath.Join(t.TempDir(), "go-builder", "b001", "package.test"))
	require.False(t, ok)
	_, ok = efdFaultySessionInvocationRootFromExecutable(filepath.Join(t.TempDir(), "go-build123", "bin", "package.test"))
	require.False(t, ok)
	_, ok = efdFaultySessionInvocationRootFromExecutable(filepath.Join(t.TempDir(), "standalone.test"))
	require.False(t, ok)
}

func TestProcessRetryEFDFaultySessionFinalizerTagsOnlyFaultyState(t *testing.T) {
	for _, test := range []struct {
		name      string
		admission earlyFlakeDetectionAdmission
		wantTag   bool
	}{
		{name: "faulty", admission: earlyFlakeDetectionAdmissionFaulty, wantTag: true},
		{name: "unavailable", admission: earlyFlakeDetectionAdmissionUnavailable},
		{name: "allowed", admission: earlyFlakeDetectionAdmissionAllowed},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder, restoreSession := setProcessRetryRecordingSessionForTesting(t)
			defer restoreSession()
			markEFDSessionFaultyIfNeeded(newTerminalEFDGuard(test.admission))
			value, tagged := recorder.GetTag(constants.TestEarlyFlakeDetectionRetryAborted)
			require.Equal(t, test.wantTag, tagged)
			if tagged {
				require.Equal(t, "faulty", value)
			}
		})
	}
}

func TestProcessRetryEFDFaultySessionConstructionBoundaries(t *testing.T) {
	threshold := func(value int) *int { return &value }
	known := &civisibilitynet.KnownTestsResponseData{Tests: civisibilitynet.KnownTestsResponseDataModules{
		"module": {"suite": {"TestKnown"}},
	}}
	tests := []struct {
		name      string
		enabled   bool
		threshold *int
		known     *civisibilitynet.KnownTestsResponseData
		wantGuard bool
		wantState earlyFlakeDetectionAdmission
	}{
		{name: "disabled"},
		{name: "absent", enabled: true},
		{name: "hundred", enabled: true, threshold: threshold(100)},
		{name: "invalid", enabled: true, threshold: threshold(-1), wantGuard: true, wantState: earlyFlakeDetectionAdmissionUnavailable},
		{name: "zero without known data", enabled: true, threshold: threshold(0), wantGuard: true, wantState: earlyFlakeDetectionAdmissionAllowed},
		{name: "missing known data", enabled: true, threshold: threshold(10)},
		{name: "active", enabled: true, threshold: threshold(10), known: known, wantGuard: true, wantState: earlyFlakeDetectionAdmissionAllowed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			settings := &civisibilitynet.SettingsResponseData{}
			settings.EarlyFlakeDetection.Enabled = tt.enabled
			settings.EarlyFlakeDetection.FaultySessionThreshold = tt.threshold
			guard := newEarlyFlakeDetectionFaultySession(settings, tt.known)
			require.Equal(t, tt.wantGuard, guard != nil)
			if guard != nil {
				require.Equal(t, tt.wantState, guard.retryState())
			}
		})
	}
}

func TestProcessRetryEFDFaultySessionAdmissionSeam(t *testing.T) {
	identity := newTestIdentity("module", "suite", "TestName")
	tests := []struct {
		name       string
		meta       *testExecutionMetadata
		admissions []earlyFlakeDetectionAdmission
		want       bool
		wantAdmit  int
		wantRetry  int
	}{
		{
			name: "known test bypasses guard",
			meta: &testExecutionMetadata{isEarlyFlakeDetectionEnabled: true, identity: identity},
			want: true,
		},
		{
			name: "attempt to fix bypasses guard",
			meta: &testExecutionMetadata{isEarlyFlakeDetectionEnabled: true, isAttemptToFix: true, isANewTest: true, identity: identity},
			want: true,
		},
		{
			name:       "new test claims once",
			meta:       &testExecutionMetadata{isEarlyFlakeDetectionEnabled: true, isANewTest: true, identity: identity},
			admissions: []earlyFlakeDetectionAdmission{earlyFlakeDetectionAdmissionAllowed, earlyFlakeDetectionAdmissionAllowed},
			want:       true, wantAdmit: 1, wantRetry: 1,
		},
		{
			name:       "modified test only observes",
			meta:       &testExecutionMetadata{isEarlyFlakeDetectionEnabled: true, isAModifiedTest: true, identity: identity},
			admissions: []earlyFlakeDetectionAdmission{earlyFlakeDetectionAdmissionFaulty},
			want:       false, wantRetry: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			guard := &recordingEFDFaultySessionGuard{admissions: append([]earlyFlakeDetectionAdmission(nil), tt.admissions...)}
			execOpts := &executionOptions{options: &runTestWithRetryOptions{efdFaultySessionGuard: guard}}
			got := admitEarlyFlakeDetectionContinuation(execOpts, nil, tt.meta)
			if tt.name == "new test claims once" {
				require.True(t, admitEarlyFlakeDetectionContinuation(execOpts, nil, tt.meta))
			}
			require.Equal(t, tt.want, got)
			require.Equal(t, tt.wantAdmit, guard.admitCalls)
			require.Equal(t, tt.wantRetry, guard.retryCalls)
		})
	}
}

func TestProcessRetryEFDFaultySessionSuppressionFallsThroughToFTROnce(t *testing.T) {
	settings := integrations.GetFlakyRetriesSettings()
	oldRetryCount := settings.RetryCount
	settings.RetryCount = 3
	t.Cleanup(func() { settings.RetryCount = oldRetryCount })

	meta := &testExecutionMetadata{isFlakyTestRetriesEnabled: true}
	retryCount, ok := transitionSuppressedEFDToFlakyRetries(meta, true, false)
	require.True(t, ok)
	require.Equal(t, int64(2), retryCount)
	require.True(t, meta.efdFellBackToFlakyRetries)

	_, ok = transitionSuppressedEFDToFlakyRetries(meta, true, false)
	require.False(t, ok, "the EFD-to-FTR transition must happen at most once")

	for _, test := range []struct {
		name      string
		failed    bool
		anyPassed bool
	}{
		{name: "passing attempt", failed: false},
		{name: "earlier pass", failed: true, anyPassed: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate := &testExecutionMetadata{isFlakyTestRetriesEnabled: true}
			_, transitioned := transitionSuppressedEFDToFlakyRetries(candidate, test.failed, test.anyPassed)
			require.False(t, transitioned)
			require.False(t, candidate.efdFellBackToFlakyRetries)
		})
	}
}

func TestProcessRetryEFDFaultySessionLockTimeoutIsDeterministic(t *testing.T) {
	directory := filepath.Join(t.TempDir(), efdFaultySessionDirectoryName)
	require.NoError(t, os.MkdirAll(directory, 0o700))
	lockPath := filepath.Join(directory, efdFaultySessionLockFile)
	require.NoError(t, os.WriteFile(lockPath, nil, 0o600))

	current := time.Unix(0, 0)
	store := &efdFaultySessionFilesystemStore{
		directory:  directory,
		threshold:  10,
		knownCount: func() uint64 { return 90 },
		now:        func() time.Time { return current },
		sleep:      func(duration time.Duration) { current = current.Add(duration) },
	}
	require.Equal(t, earlyFlakeDetectionAdmissionUnavailable, store.claim())
	contents, err := os.ReadFile(filepath.Join(directory, efdFaultySessionTerminalFile))
	require.NoError(t, err)
	require.Equal(t, "unavailable", string(contents))
	_, err = os.Stat(lockPath)
	require.NoError(t, err, "a waiter must never remove another owner's lock")
}

func TestProcessRetryEFDFaultySessionWaiterObservesPublishedTerminal(t *testing.T) {
	directory := filepath.Join(t.TempDir(), efdFaultySessionDirectoryName)
	require.NoError(t, os.MkdirAll(directory, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(directory, efdFaultySessionLockFile), nil, 0o600))

	store := &efdFaultySessionFilesystemStore{
		directory:  directory,
		threshold:  10,
		knownCount: func() uint64 { return 90 },
		now:        func() time.Time { return time.Unix(0, 0) },
		sleep: func(time.Duration) {
			require.NoError(t, os.WriteFile(filepath.Join(directory, efdFaultySessionTerminalFile), []byte("faulty"), 0o600))
		},
	}
	require.Equal(t, earlyFlakeDetectionAdmissionFaulty, store.claim())
}

func TestProcessRetryEFDFaultySessionPartialTerminalIsReread(t *testing.T) {
	directory := filepath.Join(t.TempDir(), efdFaultySessionDirectoryName)
	require.NoError(t, os.MkdirAll(directory, 0o700))
	terminalPath := filepath.Join(directory, efdFaultySessionTerminalFile)
	require.NoError(t, os.WriteFile(terminalPath, nil, 0o600))
	store := &efdFaultySessionFilesystemStore{directory: directory}

	admission, recognized := store.observe()
	require.False(t, recognized)
	require.Equal(t, earlyFlakeDetectionAdmissionUnavailable, admission)
	require.NoError(t, os.WriteFile(terminalPath, []byte("faulty"), 0o600))
	admission, recognized = store.observe()
	require.True(t, recognized)
	require.Equal(t, earlyFlakeDetectionAdmissionFaulty, admission)
}

func TestProcessRetryEFDFaultySessionRejectsCounterSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires privileges on some Windows builders")
	}
	directory := filepath.Join(t.TempDir(), efdFaultySessionDirectoryName)
	store := &efdFaultySessionFilesystemStore{
		directory:  directory,
		threshold:  1,
		knownCount: func() uint64 { return 1 },
		now:        time.Now,
		sleep:      time.Sleep,
	}
	require.Equal(t, earlyFlakeDetectionAdmissionAllowed, store.claim())
	require.NoError(t, os.Remove(filepath.Join(directory, efdFaultySessionCountFile)))
	target := filepath.Join(t.TempDir(), "counter")
	require.NoError(t, os.WriteFile(target, nil, 0o600))
	require.NoError(t, os.Symlink(target, filepath.Join(directory, efdFaultySessionCountFile)))
	_ = os.Remove(filepath.Join(directory, efdFaultySessionTerminalFile))
	require.Equal(t, earlyFlakeDetectionAdmissionUnavailable, store.claim())
}

func TestProcessRetryEFDFaultySessionStopsInlineRetries(t *testing.T) {
	createTestMetadata(t, nil)
	defer deleteTestMetadata(t)
	identity := newTestIdentity("module", "suite", "TestInlineFaultySession")
	guard := &recordingEFDFaultySessionGuard{admissions: []earlyFlakeDetectionAdmission{
		earlyFlakeDetectionAdmissionAllowed,
		earlyFlakeDetectionAdmissionFaulty,
	}}
	bodyCalls := 0
	runTestWithRetry(&runTestWithRetryOptions{
		t:                     t,
		targetFunc:            func(local *testing.T) { bodyCalls++; local.Fail() },
		efdFaultySessionGuard: guard,
		preExecMetaAdjust: func(meta *testExecutionMetadata, _ int) {
			meta.identity = identity
			meta.isEarlyFlakeDetectionEnabled = true
			meta.isANewTest = true
		},
		postAdjustRetryCount: func(*testExecutionMetadata, time.Duration) int64 { return 2 },
		preIsLastRetry:       func(_ *testExecutionMetadata, _ int, remaining int64) bool { return remaining <= 1 },
		postShouldRetry: func(_ *testing.T, _ *testExecutionMetadata, _ int, remaining int64) bool {
			return remaining >= 0
		},
	})

	require.Equal(t, 2, bodyCalls)
	require.Equal(t, 1, guard.admitCalls)
	require.Equal(t, 1, guard.retryCalls)
}

func TestProcessRetryEFDFaultySessionStopsBeforeFirstInlineRetry(t *testing.T) {
	createTestMetadata(t, nil)
	defer deleteTestMetadata(t)
	identity := newTestIdentity("module", "suite", "TestNoInlineRetry")
	guard := &recordingEFDFaultySessionGuard{admissions: []earlyFlakeDetectionAdmission{earlyFlakeDetectionAdmissionFaulty}}
	bodyCalls := 0
	runTestWithRetry(&runTestWithRetryOptions{
		t:                     t,
		targetFunc:            func(local *testing.T) { bodyCalls++; local.Fail() },
		efdFaultySessionGuard: guard,
		preExecMetaAdjust: func(meta *testExecutionMetadata, _ int) {
			meta.identity = identity
			meta.isEarlyFlakeDetectionEnabled = true
			meta.isANewTest = true
		},
		postAdjustRetryCount: func(*testExecutionMetadata, time.Duration) int64 { return 1 },
		preIsLastRetry:       func(_ *testExecutionMetadata, _ int, remaining int64) bool { return remaining <= 1 },
		postShouldRetry: func(_ *testing.T, _ *testExecutionMetadata, _ int, remaining int64) bool {
			return remaining >= 0
		},
	})

	require.Equal(t, 1, bodyCalls)
	require.Equal(t, 1, guard.admitCalls)
}

func TestProcessRetryEFDFaultySessionDoesNotAffectAttemptToFix(t *testing.T) {
	createTestMetadata(t, nil)
	defer deleteTestMetadata(t)
	identity := newTestIdentity("module", "suite", "TestAttemptToFix")
	guard := &recordingEFDFaultySessionGuard{admissions: []earlyFlakeDetectionAdmission{earlyFlakeDetectionAdmissionFaulty}}
	bodyCalls := 0
	runTestWithRetry(&runTestWithRetryOptions{
		t:                     t,
		targetFunc:            func(local *testing.T) { bodyCalls++; local.Fail() },
		efdFaultySessionGuard: guard,
		preExecMetaAdjust: func(meta *testExecutionMetadata, _ int) {
			meta.identity = identity
			meta.isEarlyFlakeDetectionEnabled = true
			meta.isANewTest = true
			meta.isAttemptToFix = true
			meta.shouldOrchestrateAttemptToFix = true
		},
		postAdjustRetryCount: func(*testExecutionMetadata, time.Duration) int64 { return 1 },
		preIsLastRetry:       func(_ *testExecutionMetadata, _ int, remaining int64) bool { return remaining <= 1 },
		postShouldRetry: func(_ *testing.T, _ *testExecutionMetadata, _ int, remaining int64) bool {
			return remaining >= 0
		},
	})

	require.Equal(t, 2, bodyCalls)
	require.Zero(t, guard.admitCalls)
	require.Zero(t, guard.retryCalls)
}

func TestProcessRetryEFDFaultySessionFallsThroughToFTR(t *testing.T) {
	createTestMetadata(t, nil)
	defer deleteTestMetadata(t)
	restoreBudget := setProcessRetryBudgetForTesting(1, 1)
	defer restoreBudget()
	identity := newTestIdentity("module", "suite", "TestFaultySessionFTR")
	guard := &recordingEFDFaultySessionGuard{admissions: []earlyFlakeDetectionAdmission{earlyFlakeDetectionAdmissionFaulty}}
	bodyCalls := 0
	fellBack := false
	runTestWithRetry(&runTestWithRetryOptions{
		t: t,
		targetFunc: func(local *testing.T) {
			bodyCalls++
			if bodyCalls == 1 {
				local.Fail()
			}
		},
		efdFaultySessionGuard: guard,
		preExecMetaAdjust: func(meta *testExecutionMetadata, _ int) {
			meta.identity = identity
			meta.isEarlyFlakeDetectionEnabled = true
			meta.isANewTest = true
			meta.isFlakyTestRetriesEnabled = true
			meta.efdFellBackToFlakyRetries = fellBack
		},
		postRetryFamilyTransition: func(meta *testExecutionMetadata) {
			fellBack = meta.efdFellBackToFlakyRetries
		},
		postAdjustRetryCount: func(*testExecutionMetadata, time.Duration) int64 { return 10 },
		preIsLastRetry:       func(_ *testExecutionMetadata, _ int, remaining int64) bool { return remaining <= 1 },
		postShouldRetry: func(local *testing.T, meta *testExecutionMetadata, _ int, remaining int64) bool {
			return willRetryAfterExecution(local.Failed(), local.Skipped(), meta, remaining, flakyRetryBudgetRemaining(integrations.GetFlakyRetriesSettings()))
		},
	})

	require.Equal(t, 2, bodyCalls)
	require.True(t, fellBack)
	require.Zero(t, flakyRetryBudgetRemaining(integrations.GetFlakyRetriesSettings()))
}

func TestProcessRetryEFDFaultySessionStopsDeferredGroupOrFallsThrough(t *testing.T) {
	t.Run("stops EFD", func(t *testing.T) {
		guard := &recordingEFDFaultySessionGuard{admissions: []earlyFlakeDetectionAdmission{earlyFlakeDetectionAdmissionFaulty}}
		group := &deferredProcessRetryGroup{
			metadata:              processRetryMetadataSnapshot{isEarlyFlakeDetectionEnabled: true, isANewTest: true},
			latest:                retryAttemptObservation{failed: true},
			efdFaultySessionGuard: guard,
		}
		group.outcomes.observe(true, false)
		require.False(t, group.admitEarlyFlakeDetectionContinuation())
	})

	t.Run("falls through to FTR", func(t *testing.T) {
		restoreBudget := setProcessRetryBudgetForTesting(2, 1)
		defer restoreBudget()
		guard := &recordingEFDFaultySessionGuard{admissions: []earlyFlakeDetectionAdmission{earlyFlakeDetectionAdmissionFaulty}}
		group := &deferredProcessRetryGroup{
			metadata: processRetryMetadataSnapshot{
				isEarlyFlakeDetectionEnabled: true,
				isANewTest:                   true,
				isFlakyTestRetriesEnabled:    true,
			},
			latest:                retryAttemptObservation{failed: true},
			parallelEFD:           true,
			efdFaultySessionGuard: guard,
		}
		group.outcomes.observe(true, false)
		require.True(t, group.admitEarlyFlakeDetectionContinuation())
		require.True(t, group.metadata.efdFellBackToFlakyRetries)
		require.False(t, group.parallelEFD)
		require.Equal(t, int64(1), group.retryCount)
		require.NotNil(t, group.reservation)
		group.reservation.refund()
	})
}

func TestProcessRetryEFDFaultySessionDeferredCoordinatorChecksBeforeLaunch(t *testing.T) {
	group := newDeferredProcessRetrySchedulerGroup("TestFaultyBeforeDrain", 2, false, false, 1, 1)
	group.efdFaultySessionGuard = &recordingEFDFaultySessionGuard{
		admissions: []earlyFlakeDetectionAdmission{earlyFlakeDetectionAdmissionFaulty},
	}
	runnerCalls := 0
	coordinator := newProcessRetryCoordinatorForTesting(false, func(context.Context, *deferredProcessRetryGroup, deferredProcessRetryPreparedAttempt) processRetryAttemptResult {
		runnerCalls++
		return processRetryAttemptResult{}
	})
	require.True(t, coordinator.beginAdmission().commit(group))
	summary := coordinator.drain(0)
	require.Zero(t, runnerCalls)
	require.True(t, summary.deferredFailed, "the completed first-attempt failure remains authoritative")
}

func TestProcessRetryEFDFaultySessionDeferredCoordinatorStopsAfterRunningAttempt(t *testing.T) {
	_, restoreSession := setProcessRetryRecordingSessionForTesting(t)
	defer restoreSession()
	group := newDeferredProcessRetrySchedulerGroup("TestFaultyAfterAttempt", 2, false, false, 1, 1)
	group.efdFaultySessionGuard = &recordingEFDFaultySessionGuard{
		admissions: []earlyFlakeDetectionAdmission{
			earlyFlakeDetectionAdmissionAllowed,
			earlyFlakeDetectionAdmissionFaulty,
		},
	}
	runnerCalls := 0
	coordinator := newProcessRetryCoordinatorForTesting(false, func(_ context.Context, _ *deferredProcessRetryGroup, prepared deferredProcessRetryPreparedAttempt) processRetryAttemptResult {
		runnerCalls++
		attempt := deferredProcessRetryPassingAttempt(prepared.index)
		attempt.Result.Status = processRetryStatusFail
		attempt.ExitCode = processRetryFailureExitCode
		return attempt
	})
	require.True(t, coordinator.beginAdmission().commit(group))
	_ = coordinator.drain(0)
	require.Equal(t, 1, runnerCalls)
}

func TestProcessRetryEFDFaultySessionDeferredCoordinatorFallsThroughToFTR(t *testing.T) {
	_, restoreSession := setProcessRetryRecordingSessionForTesting(t)
	defer restoreSession()
	restoreBudget := setProcessRetryBudgetForTesting(1, 1)
	defer restoreBudget()
	group := newDeferredProcessRetrySchedulerGroup("TestFaultyDeferredFTR", 10, true, false, 1, 2)
	group.metadata.isFlakyTestRetriesEnabled = true
	group.efdFaultySessionGuard = &recordingEFDFaultySessionGuard{
		admissions: []earlyFlakeDetectionAdmission{earlyFlakeDetectionAdmissionFaulty},
	}
	var retryReason string
	coordinator := newProcessRetryCoordinatorForTesting(false, func(_ context.Context, _ *deferredProcessRetryGroup, prepared deferredProcessRetryPreparedAttempt) processRetryAttemptResult {
		retryReason = prepared.retryReason
		return deferredProcessRetryPassingAttempt(prepared.index)
	})
	require.True(t, coordinator.beginAdmission().commit(group))
	_ = coordinator.drain(0)
	require.Equal(t, constants.AutoTestRetriesRetryReason, retryReason)
	require.True(t, group.metadata.efdFellBackToFlakyRetries)
	require.False(t, group.parallelEFD)
	require.Zero(t, flakyRetryBudgetRemaining(integrations.GetFlakyRetriesSettings()))
}

func TestProcessRetryEFDFaultySessionParallelEFDWaitsForActiveAttemptsBeforeFTR(t *testing.T) {
	_, restoreSession := setProcessRetryRecordingSessionForTesting(t)
	defer restoreSession()
	restoreBudget := setProcessRetryBudgetForTesting(1, 1)
	defer restoreBudget()

	group := newDeferredProcessRetrySchedulerGroup("TestParallelFaultySessionFTR", 4, true, false, 1, 2)
	group.metadata.isFlakyTestRetriesEnabled = true
	group.efdFaultySessionGuard = &recordingEFDFaultySessionGuard{admissions: []earlyFlakeDetectionAdmission{
		earlyFlakeDetectionAdmissionAllowed,
		earlyFlakeDetectionAdmissionAllowed,
		earlyFlakeDetectionAdmissionFaulty,
		earlyFlakeDetectionAdmissionFaulty,
	}}

	started := make(chan deferredProcessRetryPreparedAttempt, 3)
	releaseEFD := make(chan struct{})
	coordinator := newProcessRetryCoordinatorForTesting(false, func(_ context.Context, _ *deferredProcessRetryGroup, prepared deferredProcessRetryPreparedAttempt) processRetryAttemptResult {
		started <- prepared
		attempt := deferredProcessRetryPassingAttempt(prepared.index)
		if prepared.retryReason == constants.EarlyFlakeDetectionRetryReason {
			<-releaseEFD
			attempt.Result.Status = processRetryStatusFail
			attempt.ExitCode = processRetryFailureExitCode
		}
		return attempt
	})
	require.True(t, coordinator.beginAdmission().commit(group))

	drained := make(chan processRetryCoordinatorSummary, 1)
	go func() { drained <- coordinator.drain(0) }()
	first := <-started
	second := <-started
	require.Equal(t, constants.EarlyFlakeDetectionRetryReason, first.retryReason)
	require.Equal(t, constants.EarlyFlakeDetectionRetryReason, second.retryReason)
	close(releaseEFD)

	third := <-started
	require.Equal(t, constants.AutoTestRetriesRetryReason, third.retryReason)
	summary := <-drained
	require.False(t, summary.packageFailed)
	require.True(t, group.metadata.efdFellBackToFlakyRetries)
	require.False(t, group.parallelEFD)
}

func TestProcessRetryEFDFaultySessionParallelTerminalAttemptPreventsFTR(t *testing.T) {
	_, restoreSession := setProcessRetryRecordingSessionForTesting(t)
	defer restoreSession()
	restoreBudget := setProcessRetryBudgetForTesting(1, 1)
	defer restoreBudget()

	faultyObserved := make(chan struct{})
	group := newDeferredProcessRetrySchedulerGroup("TestParallelFaultySessionTerminal", 4, true, false, 1, 2)
	group.metadata.isFlakyTestRetriesEnabled = true
	group.efdFaultySessionGuard = &recordingEFDFaultySessionGuard{
		admissions: []earlyFlakeDetectionAdmission{
			earlyFlakeDetectionAdmissionAllowed,
			earlyFlakeDetectionAdmissionAllowed,
			earlyFlakeDetectionAdmissionFaulty,
		},
		faultyObserved: faultyObserved,
	}

	started := make(chan int, 2)
	releaseTerminal := make(chan struct{})
	releaseFailure := make(chan struct{})
	var runnerCalls atomic.Int32
	coordinator := newProcessRetryCoordinatorForTesting(false, func(_ context.Context, _ *deferredProcessRetryGroup, prepared deferredProcessRetryPreparedAttempt) processRetryAttemptResult {
		runnerCalls.Add(1)
		started <- prepared.index
		attempt := deferredProcessRetryPassingAttempt(prepared.index)
		switch prepared.index {
		case 1:
			<-releaseTerminal
			attempt.TimedOut = true
		case 2:
			<-releaseFailure
			attempt.Result.Status = processRetryStatusFail
			attempt.ExitCode = processRetryFailureExitCode
		default:
			t.Fatalf("unexpected retry attempt %d", prepared.index)
		}
		return attempt
	})
	require.True(t, coordinator.beginAdmission().commit(group))

	drained := make(chan processRetryCoordinatorSummary, 1)
	go func() { drained <- coordinator.drain(0) }()
	indices := []int{<-started, <-started}
	slices.Sort(indices)
	require.Equal(t, []int{1, 2}, indices)

	// Complete the later ordinary failure first so the coordinator observes the
	// faulty-session stop while the earlier terminal attempt remains active.
	close(releaseFailure)
	<-faultyObserved
	close(releaseTerminal)

	summary := <-drained
	require.Equal(t, int32(2), runnerCalls.Load())
	require.True(t, summary.packageFailed)
	require.False(t, group.metadata.efdFellBackToFlakyRetries)
}

func BenchmarkProcessRetryEFDFaultySession(b *testing.B) {
	knownData := func(count int) *civisibilitynet.KnownTestsResponseData {
		tests := make([]string, count)
		for index := range count {
			tests[index] = "TestKnown" + strconv.Itoa(index)
		}
		return &civisibilitynet.KnownTestsResponseData{Tests: civisibilitynet.KnownTestsResponseDataModules{
			"module": {"suite": tests},
		}}
	}
	settings := func(threshold int) *civisibilitynet.SettingsResponseData {
		data := &civisibilitynet.SettingsResponseData{}
		data.EarlyFlakeDetection.Enabled = true
		data.EarlyFlakeDetection.FaultySessionThreshold = &threshold
		return data
	}

	for _, count := range []int{100, 10_000, 100_000} {
		known := knownData(count)
		b.Run("construct/known="+strconv.Itoa(count), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if newEarlyFlakeDetectionFaultySession(settings(10), known) == nil {
					b.Fatal("guard was not constructed")
				}
			}
		})
		b.Run("count-known/known="+strconv.Itoa(count), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if got := countUniqueKnownTopLevelTests(known); got != uint64(count) {
					b.Fatalf("known count = %d, want %d", got, count)
				}
			}
		})
		b.Run("initialize-shared/known="+strconv.Itoa(count), func(b *testing.B) {
			base := b.TempDir()
			var sequence atomic.Uint64
			b.ReportAllocs()
			for b.Loop() {
				store := &efdFaultySessionFilesystemStore{
					directory:  filepath.Join(base, strconv.FormatUint(sequence.Add(1), 10)),
					threshold:  10,
					knownCount: func() uint64 { return countUniqueKnownTopLevelTests(known) },
					now:        time.Now,
					sleep:      time.Sleep,
				}
				if store.claim() != earlyFlakeDetectionAdmissionAllowed {
					b.Fatal("shared store initialization was not admitted")
				}
			}
		})
	}

	b.Run("known-fast-path", func(b *testing.B) {
		meta := &testExecutionMetadata{isEarlyFlakeDetectionEnabled: true, identity: newTestIdentity("module", "suite", "TestKnown")}
		execOpts := &executionOptions{options: &runTestWithRetryOptions{efdFaultySessionGuard: newTerminalEFDGuard(earlyFlakeDetectionAdmissionFaulty)}}
		b.ReportAllocs()
		for b.Loop() {
			if !admitEarlyFlakeDetectionContinuation(execOpts, nil, meta) {
				b.Fatal("known test consulted faulty-session state")
			}
		}
	})

	b.Run("repeated-identity", func(b *testing.B) {
		store := &efdFaultySessionLocalStore{threshold: 99, knownCount: func() uint64 { return 1_000_000 }}
		guard := &efdFaultySessionGuard{identities: make(map[efdFaultySessionIdentity]*efdFaultySessionIdentityEntry), store: store}
		identity := newTestIdentity("module", "suite", "TestNew")
		require.Equal(b, earlyFlakeDetectionAdmissionAllowed, guard.admitNewTest(identity))
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			if guard.admitNewTest(identity) != earlyFlakeDetectionAdmissionAllowed {
				b.Fatal("repeated identity was not admitted")
			}
		}
	})

	b.Run("uncontended-local-claim", func(b *testing.B) {
		store := &efdFaultySessionLocalStore{threshold: 99, knownCount: func() uint64 { return 1_000_000 }}
		b.ReportAllocs()
		for b.Loop() {
			if store.claim() != earlyFlakeDetectionAdmissionAllowed {
				b.Fatal("local claim was not admitted")
			}
		}
	})

	b.Run("terminal-fast-path", func(b *testing.B) {
		guard := newTerminalEFDGuard(earlyFlakeDetectionAdmissionFaulty)
		b.ReportAllocs()
		for b.Loop() {
			if guard.retryState() != earlyFlakeDetectionAdmissionFaulty {
				b.Fatal("terminal state changed")
			}
		}
	})

	b.Run("threshold-crossing", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			store := &efdFaultySessionLocalStore{threshold: 1, knownCount: func() uint64 { return 1 }}
			if store.claim() != earlyFlakeDetectionAdmissionAllowed || store.claim() != earlyFlakeDetectionAdmissionFaulty {
				b.Fatal("local store did not cross at L+1")
			}
		}
	})

	for _, parallelism := range []int{2, 8, 32} {
		b.Run("contended-local/parallel="+strconv.Itoa(parallelism), func(b *testing.B) {
			store := &efdFaultySessionLocalStore{threshold: 99, knownCount: func() uint64 { return 1_000_000_000 }}
			b.SetParallelism(parallelism)
			b.ReportAllocs()
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					if store.claim() != earlyFlakeDetectionAdmissionAllowed {
						b.Fatal("contended claim was not admitted")
					}
				}
			})
		})
	}

	b.Run("work-root-discovery", func(b *testing.B) {
		executable := filepath.Join(string(filepath.Separator), "tmp", "go-build123", "b001", "package.test")
		b.ReportAllocs()
		for b.Loop() {
			if _, ok := efdFaultySessionInvocationRootFromExecutable(executable); !ok {
				b.Fatal("work root was not discovered")
			}
		}
	})
}
