// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package config

import (
	"fmt"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal/globalconfig"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/processtags"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/telemetrytest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type callbackLogger struct {
	once     sync.Once
	callback func()
}

func (l *callbackLogger) Log(string) {
	l.once.Do(l.callback)
}

func TestPublicationHandoffReentrantFreshGetAndCreateNewDoesNotBlock(t *testing.T) {
	resetGlobalState()
	t.Cleanup(resetGlobalState)
	SetUseFreshConfig(true)

	staged := NewTracerGeneration()
	staged.SetHeaderAsTags([]string{"X-Published:published.tag"}, OriginCode, ProductTracer)

	done := make(chan error, 1)
	go func() {
		done <- PublishTracerGeneration(staged, staged.PrepareClaims(), func(publication Publication) {
			assert.Same(t, staged, Get())
			assert.Same(t, staged, CreateNew())
			assert.True(t, publication.ApplyHeaderAsTags())
		})
	}()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("publication handoff waited on its own coordinator state")
	}
	assert.Equal(t, "published.tag", globalconfig.HeaderTag("X-Published"))
}

func TestPublicationNeedsNoCallerCompletionToReleaseCoordinator(t *testing.T) {
	resetGlobalState()
	t.Cleanup(resetGlobalState)

	first := NewTracerGeneration()
	require.NoError(t, PublishTracerGeneration(first, first.PrepareClaims(), nil))

	second := NewTracerGeneration()
	done := make(chan error, 1)
	go func() {
		done <- PublishTracerGeneration(second, second.PrepareClaims(), nil)
	}()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("completed publication left coordinator ownership behind")
	}
	assert.Same(t, second, Get())
}

func TestPublicationPanicReleasesCoordinator(t *testing.T) {
	resetGlobalState()
	t.Cleanup(resetGlobalState)

	first := NewTracerGeneration()
	func() {
		defer func() {
			assert.Equal(t, "handoff panic", recover())
		}()
		_ = PublishTracerGeneration(first, first.PrepareClaims(), func(Publication) {
			panic("handoff panic")
		})
	}()
	assert.Same(t, first, Get())

	second := NewTracerGeneration()
	require.NoError(t, PublishTracerGeneration(second, second.PrepareClaims(), nil))
	assert.Same(t, second, Get())
}

func TestNestedTracerPublicationReturnsBusyWithoutConsumingCandidate(t *testing.T) {
	resetGlobalState()
	t.Cleanup(resetGlobalState)

	first := NewTracerGeneration()
	second := NewTracerGeneration()
	secondPrepared := second.PrepareClaims()

	require.NoError(t, PublishTracerGeneration(first, first.PrepareClaims(), func(Publication) {
		err := PublishTracerGeneration(second, secondPrepared, nil)
		require.ErrorIs(t, err, errPublicationBusy)
		assert.Same(t, first, Get())
	}))

	require.NoError(t, PublishTracerGeneration(second, secondPrepared, nil))
	assert.Same(t, second, Get())
}

func TestSameGenerationHeaderUpdateWinsAgainstPausedInitialSnapshot(t *testing.T) {
	resetGlobalState()
	t.Cleanup(resetGlobalState)
	globalconfig.ClearHeaderTags()
	t.Cleanup(globalconfig.ClearHeaderTags)

	originalPrepare := prepareHeaderAsTagsForRegistry
	initialPreparing := make(chan struct{})
	releaseInitial := make(chan struct{})
	var once sync.Once
	prepareHeaderAsTagsForRegistry = func(header []string) preparedHeaderTags {
		if assert.ObjectsAreEqual([]string{"X-Initial:initial.tag"}, header) {
			once.Do(func() { close(initialPreparing) })
			<-releaseInitial
		}
		return originalPrepare(header)
	}
	t.Cleanup(func() { prepareHeaderAsTagsForRegistry = originalPrepare })

	staged := NewTracerGeneration()
	staged.SetHeaderAsTags([]string{"X-Initial:initial.tag"}, OriginCode, ProductTracer)
	done := make(chan error, 1)
	go func() {
		done <- PublishTracerGeneration(staged, staged.PrepareClaims(), func(publication Publication) {
			publication.ApplyHeaderAsTags()
		})
	}()
	<-initialPreparing

	latest := []string{"X-Latest:latest.tag"}
	require.True(t, staged.HeaderAsTagsConfig().HandleRC(&latest))
	close(releaseInitial)
	require.NoError(t, <-done)

	assert.Equal(t, "latest.tag", globalconfig.HeaderTag("X-Latest"))
	assert.Empty(t, globalconfig.HeaderTag("X-Initial"))
}

func TestContainerTagsHashUsesGenerationAndRevisionFence(t *testing.T) {
	resetGlobalState()
	t.Cleanup(resetGlobalState)
	oldHash := processtags.ContainerTagsHash()
	t.Cleanup(func() { processtags.SetContainerTagsHash(oldHash) })

	first := NewTracerGeneration()
	require.NoError(t, PublishTracerGeneration(first, first.PrepareClaims(), func(publication Publication) {
		require.True(t, publication.ApplyContainerTagsHash(1, "first"))
		require.True(t, publication.ApplyContainerTagsHash(3, "first-latest"))
		assert.False(t, publication.ApplyContainerTagsHash(2, "first-stale"))
	}))
	assert.Equal(t, "first-latest", processtags.ContainerTagsHash())

	second := NewTracerGeneration()
	require.NoError(t, PublishTracerGeneration(second, second.PrepareClaims(), func(publication Publication) {
		require.True(t, publication.ApplyContainerTagsHash(1, "second"))
	}))
	assert.Equal(t, "second", processtags.ContainerTagsHash())

	firstPublication := Publication{
		store:         globalStore,
		config:        first,
		publicationID: first.publicationID.Load(),
		generation:    first.generation.Load(),
	}
	assert.False(t, firstPublication.ApplyContainerTagsHash(4, "retired"))
	assert.Equal(t, "second", processtags.ContainerTagsHash())
}

func TestHeaderSetterDuringPublicationAppliesLatestValue(t *testing.T) {
	resetGlobalState()
	t.Cleanup(resetGlobalState)
	globalconfig.ClearHeaderTags()
	t.Cleanup(globalconfig.ClearHeaderTags)

	staged := NewTracerGeneration()
	staged.SetHeaderAsTags([]string{"X-Initial:initial.tag"}, OriginCode, ProductTracer)
	latest := []string{"X-Latest:latest.tag"}
	err := PublishTracerGeneration(staged, staged.PrepareClaims(), func(publication Publication) {
		require.True(t, staged.HeaderAsTagsConfig().HandleRC(&latest))
		assert.False(t, publication.ApplyHeaderAsTags(),
			"the remote-config setter already applied this same-generation revision")
	})
	require.NoError(t, err)
	assert.Equal(t, "latest.tag", globalconfig.HeaderTag("X-Latest"))
	assert.Empty(t, globalconfig.HeaderTag("X-Initial"))
}

func TestLateHeaderRemoteConfigLoggingCanReenterFreshPublication(t *testing.T) {
	resetGlobalState()
	t.Cleanup(resetGlobalState)
	SetUseFreshConfig(true)
	globalconfig.ClearHeaderTags()
	t.Cleanup(globalconfig.ClearHeaderTags)

	oldLevel := log.GetLevel()
	log.SetLevel(log.LevelDebug)
	t.Cleanup(func() { log.SetLevel(oldLevel) })

	staged := NewTracerGeneration()
	staged.SetHeaderAsTags([]string{"X-Initial:initial.tag"}, OriginCode, ProductTracer)
	reentered := make(chan struct{})
	restoreLogger := log.UseLogger(&callbackLogger{callback: func() {
		assert.Same(t, staged, Get())
		assert.Same(t, staged, CreateNew())
		close(reentered)
	}})
	t.Cleanup(restoreLogger)

	done := make(chan error, 1)
	go func() {
		done <- PublishTracerGeneration(staged, staged.PrepareClaims(), func(Publication) {
			invalid := []string{"X-Invalid:"}
			require.True(t, staged.HeaderAsTagsConfig().HandleRC(&invalid))
		})
	}()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("late header remote-config logging deadlocked during fresh publication re-entry")
	}
	select {
	case <-reentered:
	case <-time.After(time.Second):
		t.Fatal("invalid-header logger callback did not run")
	}
	assert.Empty(t, globalconfig.HeaderTag("X-Initial"))
}

func TestHeaderTargetApplyDoesNotInvertHeaderIterationAndStore(t *testing.T) {
	resetGlobalState()
	t.Cleanup(resetGlobalState)
	globalconfig.ClearHeaderTags()
	t.Cleanup(globalconfig.ClearHeaderTags)

	first := NewTracerGeneration()
	first.SetHeaderAsTags([]string{"X-First:first.tag"}, OriginCode, ProductTracer)
	require.NoError(t, publishGenerationForTest(first, first.PrepareClaims()))

	originalApply := applyHeaderTagsForPublication
	targetEntered := make(chan struct{})
	var once sync.Once
	applyHeaderTagsForPublication = func(publicationID, generation, revision uint64, prepared preparedHeaderTags) bool {
		once.Do(func() { close(targetEntered) })
		return originalApply(publicationID, generation, revision, prepared)
	}
	t.Cleanup(func() { applyHeaderTagsForPublication = originalApply })

	iterEntered := make(chan struct{})
	iterDone := make(chan struct{})
	go func() {
		globalconfig.HeaderTagMap().Iter(func(string, string) {
			close(iterEntered)
			<-targetEntered
			assert.NotNil(t, Get())
		})
		close(iterDone)
	}()
	<-iterEntered

	second := NewTracerGeneration()
	second.SetHeaderAsTags([]string{"X-Second:second.tag"}, OriginCode, ProductTracer)
	publishDone := make(chan error, 1)
	go func() {
		publishDone <- publishGenerationForTest(second, second.PrepareClaims())
	}()

	select {
	case err := <-publishDone:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("header target application deadlocked with HeaderTagMap.Iter callback re-entering Store.Get")
	}
	select {
	case <-iterDone:
	case <-time.After(time.Second):
		t.Fatal("HeaderTagMap.Iter callback remained blocked after publication")
	}
	assert.Equal(t, "second.tag", globalconfig.HeaderTag("X-Second"))
}

func TestContainerTargetSetterCanProbeStoreWithoutStoreLock(t *testing.T) {
	resetGlobalState()
	t.Cleanup(resetGlobalState)

	staged := NewTracerGeneration()
	originalApply := applyContainerTagsHashForPublication
	probed := make(chan struct{})
	var probeOnce sync.Once
	applyContainerTagsHashForPublication = func(publicationID, generation, revision uint64, hash string) bool {
		assert.Same(t, staged, Get())
		probeOnce.Do(func() { close(probed) })
		return originalApply(publicationID, generation, revision, hash)
	}
	t.Cleanup(func() { applyContainerTagsHashForPublication = originalApply })

	done := make(chan error, 1)
	go func() {
		done <- PublishTracerGeneration(staged, staged.PrepareClaims(), func(publication Publication) {
			require.True(t, publication.ApplyContainerTagsHash(1, "probe"))
		})
	}()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("container target setter ran while the Store mutex was held")
	}
	select {
	case <-probed:
	case <-time.After(time.Second):
		t.Fatal("container target setter was not called")
	}
}

func TestNewPublicationFencesPausedContainerUpdateWithEmptyHash(t *testing.T) {
	resetGlobalState()
	t.Cleanup(resetGlobalState)
	oldHash := processtags.ContainerTagsHash()
	t.Cleanup(func() { processtags.SetContainerTagsHash(oldHash) })

	first := NewTracerGeneration()
	var firstPublication Publication
	require.NoError(t, PublishTracerGeneration(first, first.PrepareClaims(), func(publication Publication) {
		firstPublication = publication
		require.True(t, publication.ApplyContainerTagsHash(1, "first"))
	}))

	originalApply := applyContainerTagsHashForPublication
	oldTargetEntered := make(chan struct{})
	releaseOldTarget := make(chan struct{})
	applyContainerTagsHashForPublication = func(publicationID, generation, revision uint64, hash string) bool {
		if publicationID == firstPublication.publicationID && hash == "stale" {
			close(oldTargetEntered)
			<-releaseOldTarget
		}
		return originalApply(publicationID, generation, revision, hash)
	}
	t.Cleanup(func() { applyContainerTagsHashForPublication = originalApply })

	oldApplied := make(chan bool, 1)
	go func() {
		oldApplied <- firstPublication.ApplyContainerTagsHash(2, "stale")
	}()
	<-oldTargetEntered

	second := NewTracerGeneration()
	require.NoError(t, PublishTracerGeneration(second, second.PrepareClaims(), nil))
	close(releaseOldTarget)

	assert.False(t, <-oldApplied)
	assert.Empty(t, processtags.ContainerTagsHash(),
		"a no-hash generation must fence and clear an older delayed target write")
}

func TestHeaderPublicationReadsValueAndRevisionAtomically(t *testing.T) {
	resetGlobalState()
	t.Cleanup(resetGlobalState)
	globalconfig.ClearHeaderTags()
	t.Cleanup(globalconfig.ClearHeaderTags)

	staged := NewTracerGeneration()
	staged.SetHeaderAsTags([]string{"X-Initial:initial.tag"}, OriginCode, ProductTracer)
	staged.headerSnapshotMu.RLock()

	originalApply := applyHeaderTagsForPublication
	type appliedHeader struct {
		revision uint64
		tag      string
	}
	var (
		appliedMu sync.Mutex
		applied   []appliedHeader
	)
	applyHeaderTagsForPublication = func(publicationID, generation, revision uint64, prepared preparedHeaderTags) bool {
		tag := ""
		if len(prepared.tags) > 0 {
			tag = prepared.tags[0].tag
		}
		appliedMu.Lock()
		applied = append(applied, appliedHeader{revision: revision, tag: tag})
		appliedMu.Unlock()
		return originalApply(publicationID, generation, revision, prepared)
	}
	t.Cleanup(func() { applyHeaderTagsForPublication = originalApply })

	committed := make(chan struct{})
	applyPublication := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- PublishTracerGeneration(staged, staged.PrepareClaims(), func(publication Publication) {
			close(committed)
			<-applyPublication
			publication.ApplyHeaderAsTags()
		})
	}()
	<-committed

	latest := []string{"X-Latest:latest.tag"}
	rcDone := make(chan bool, 1)
	go func() {
		rcDone <- staged.HeaderAsTagsConfig().HandleRC(&latest)
	}()
	require.Eventually(t, func() bool {
		return assert.ObjectsAreEqual(latest, staged.HeaderAsTags())
	}, time.Second, time.Millisecond)

	close(applyPublication)
	staged.headerSnapshotMu.RUnlock()
	require.True(t, <-rcDone)
	require.NoError(t, <-done)

	_, latestRevision := staged.headerSnapshot()
	appliedMu.Lock()
	defer appliedMu.Unlock()
	for _, call := range applied {
		if call.tag == "latest.tag" {
			assert.Equal(t, latestRevision, call.revision,
				"the RC value must never be paired with an older header revision")
		}
	}
	assert.Equal(t, "latest.tag", globalconfig.HeaderTag("X-Latest"))
	assert.Empty(t, globalconfig.HeaderTag("X-Initial"))
}

func publishGenerationForTest(c *Config, prepared PreparedClaims) error {
	return PublishTracerGeneration(c, prepared, func(publication Publication) {
		publication.ApplyHeaderAsTags()
		c.DrainPublicationTelemetry()
	})
}

func TestStagedGenerationTelemetryDrainsOnlyAfterPublication(t *testing.T) {
	resetGlobalState()
	t.Cleanup(resetGlobalState)
	rec := new(telemetrytest.RecordClient)
	t.Cleanup(telemetry.MockClient(rec))

	staged := NewTracerGeneration()
	staged.SetServiceName("published-service", OriginCode, ProductTracer)
	require.Empty(t, rec.Configuration, "an unpublished generation must not emit configuration telemetry")

	err := PublishTracerGeneration(staged, staged.PrepareClaims(), func(publication Publication) {
		require.Empty(t, rec.Configuration, "the Store commit must not synchronously drain publication telemetry")
		publication.ApplyHeaderAsTags()
		staged.DrainPublicationTelemetry()
	})
	require.NoError(t, err)
	require.Equal(t, 1, countConfiguration(rec.Configuration, "DD_SERVICE", telemetry.OriginCode, "published-service"))

	assert.Same(t, staged, Get())
	require.Equal(t, 1, countConfiguration(rec.Configuration, "DD_SERVICE", telemetry.OriginCode, "published-service"),
		"publication telemetry must drain exactly once")
}

func TestFailedGenerationPublicationDiscardsTelemetry(t *testing.T) {
	resetGlobalState()
	t.Cleanup(resetGlobalState)
	rec := new(telemetrytest.RecordClient)
	t.Cleanup(telemetry.MockClient(rec))

	staged := NewTracerGeneration()
	staged.SetServiceName("discarded-service", OriginCode, ProductTracer)
	prepared := staged.PrepareClaims()
	staged.SetEnv("invalidate-prepared-token", OriginCode, ProductTracer)

	err := PublishTracerGeneration(staged, prepared, nil)
	require.Error(t, err)
	require.Zero(t, countConfiguration(rec.Configuration, "DD_SERVICE", telemetry.OriginCode, "discarded-service"))
}

func TestStagedDynamicConfigTelemetryWaitsForPublication(t *testing.T) {
	resetGlobalState()
	t.Cleanup(resetGlobalState)
	rec := new(telemetrytest.RecordClient)
	t.Cleanup(telemetry.MockClient(rec))

	staged := NewTracerGeneration()
	rate := 0.25
	require.True(t, staged.GlobalSampleRateConfig().HandleRC(&rate))
	require.Zero(t, countConfiguration(rec.Configuration, "trace_sample_rate", telemetry.OriginRemoteConfig, rate))

	err := PublishTracerGeneration(staged, staged.PrepareClaims(), func(Publication) {
		staged.DrainPublicationTelemetry()
	})
	require.NoError(t, err)
	require.Equal(t, 1, countConfiguration(rec.Configuration, "trace_sample_rate", telemetry.OriginRemoteConfig, rate))
}

func countConfiguration(configs []telemetry.Configuration, name string, origin telemetry.Origin, value any) int {
	count := 0
	for _, cfg := range configs {
		if cfg.Name == name && cfg.Origin == origin && cfg.Value == value {
			count++
		}
	}
	return count
}

func TestGetConcurrentFirstUsePublishesOneNonNilBaseline(t *testing.T) {
	resetGlobalState()
	t.Cleanup(resetGlobalState)

	const goroutines = 32
	configs := make([]*Config, goroutines)
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := range configs {
		go func() {
			defer wg.Done()
			configs[i] = Get()
		}()
	}
	wg.Wait()

	require.NotNil(t, configs[0])
	for _, cfg := range configs[1:] {
		assert.Same(t, configs[0], cfg)
	}
}

func TestConcurrentFirstGetReportsOnlyWinningCandidateTelemetry(t *testing.T) {
	resetGlobalState()
	t.Cleanup(resetGlobalState)
	rec := new(telemetrytest.RecordClient)
	t.Cleanup(telemetry.MockClient(rec))

	const goroutines = 16
	originalLoad := loadConfigForStore
	t.Cleanup(func() { loadConfigForStore = originalLoad })
	var resolved atomic.Int32
	allResolved := make(chan struct{})
	loadConfigForStore = func() *Config {
		cfg := loadConfig()
		if resolved.Add(1) == goroutines {
			close(allResolved)
		}
		<-allResolved
		return cfg
	}

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			Get()
		}()
	}
	wg.Wait()

	require.Equal(t, 1, countConfiguration(rec.Configuration, "DD_SERVICE", telemetry.OriginDefault, ""),
		"losing first-use candidates must discard their buffered telemetry")
}

func TestPublishRejectsClaimRevisionConflict(t *testing.T) {
	resetGlobalState()
	t.Cleanup(resetGlobalState)

	staged := NewTracerGeneration()
	staged.SetServiceName("tracer", OriginCode, ProductTracer)
	prepared := staged.PrepareClaims()
	release, accepted := AcquireProductClaims(ProductProfiler, []Claim{{
		Name: "DD_SERVICE", Value: "profiler",
	}})
	defer release()
	require.True(t, accepted["DD_SERVICE"])
	require.Error(t, publishGenerationForTest(staged, prepared))
	require.NotSame(t, staged, Get())
}

func TestClaimExistingConflictRevertsStagedValueToSourceBaseline(t *testing.T) {
	resetGlobalState()
	t.Cleanup(resetGlobalState)
	t.Setenv("DD_SERVICE", "from-env")

	release, accepted := AcquireProductClaims(ProductProfiler, []Claim{{
		Name: "DD_SERVICE", Value: "profiler",
	}})
	defer release()
	require.True(t, accepted["DD_SERVICE"])

	staged := NewTracerGeneration()
	staged.SetServiceName("tracer", OriginCode, ProductTracer)
	prepared := staged.PrepareClaims()

	assert.Equal(t, "from-env", staged.ServiceName())
	require.NoError(t, publishGenerationForTest(staged, prepared))
	assert.Same(t, staged, Get())

	_, tracerAccepted := AcquireProductClaims(ProductProfiler, []Claim{{
		Name: "DD_SERVICE", Value: "different",
	}})
	assert.False(t, tracerAccepted["DD_SERVICE"], "the profiler must retain the first-in claim")
}

func TestClaimSameValueHoldersAndStaleRelease(t *testing.T) {
	resetGlobalState()
	t.Cleanup(resetGlobalState)

	releaseFirst, accepted := AcquireProductClaims(ProductProfiler, []Claim{{
		Name: "DD_SERVICE", Value: "shared",
	}})
	require.True(t, accepted["DD_SERVICE"])
	releaseSecond, accepted := AcquireProductClaims(ProductProfiler, []Claim{{
		Name: "DD_SERVICE", Value: "shared",
	}})
	require.True(t, accepted["DD_SERVICE"])

	releaseFirst()
	releaseFirst()
	releaseConflict, accepted := AcquireProductClaims(ProductProfiler, []Claim{{
		Name: "DD_SERVICE", Value: "different",
	}})
	require.False(t, accepted["DD_SERVICE"], "the second same-value holder must keep the claim active")
	releaseConflict()

	releaseSecond()
	releaseReplacement, accepted := AcquireProductClaims(ProductProfiler, []Claim{{
		Name: "DD_SERVICE", Value: "replacement",
	}})
	require.True(t, accepted["DD_SERVICE"])
	defer releaseReplacement()

	releaseFirst()
	releaseConflict, accepted = AcquireProductClaims(ProductProfiler, []Claim{{
		Name: "DD_SERVICE", Value: "stale-release-must-not-win",
	}})
	defer releaseConflict()
	assert.False(t, accepted["DD_SERVICE"])
}

func TestClaimReleaseFromReplacedStoreCannotDeleteReusedLease(t *testing.T) {
	resetGlobalState()
	t.Cleanup(resetGlobalState)

	staleRelease, accepted := AcquireProductClaims(ProductProfiler, []Claim{{
		Name: "DD_SERVICE", Value: "old-store",
	}})
	require.True(t, accepted["DD_SERVICE"])

	resetGlobalState()
	currentRelease, accepted := AcquireProductClaims(ProductProfiler, []Claim{{
		Name: "DD_SERVICE", Value: "new-store",
	}})
	t.Cleanup(currentRelease)
	require.True(t, accepted["DD_SERVICE"])

	staleRelease()
	conflictRelease, accepted := AcquireProductClaims(ProductProfiler, []Claim{{
		Name: "DD_SERVICE", Value: "must-not-replace-current-lease",
	}})
	t.Cleanup(conflictRelease)
	assert.False(t, accepted["DD_SERVICE"])
}

func TestPublishReplacesActiveTracerClaims(t *testing.T) {
	resetGlobalState()
	t.Cleanup(resetGlobalState)

	first := NewTracerGeneration()
	first.SetServiceName("first", OriginCode, ProductTracer)
	require.NoError(t, publishGenerationForTest(first, first.PrepareClaims()))

	second := NewTracerGeneration()
	second.SetServiceName("second", OriginCode, ProductTracer)
	prepared := second.PrepareClaims()
	assert.Equal(t, "second", second.ServiceName(), "the active tracer must not conflict with its replacement")
	require.NoError(t, publishGenerationForTest(second, prepared))

	assert.Same(t, second, Get())
	assert.Equal(t, "first", first.ServiceName())
	assert.Equal(t, "second", second.ServiceName())
}

func TestRetiredGenerationReadsAndRemoteConfigStayPinned(t *testing.T) {
	resetGlobalState()
	t.Cleanup(resetGlobalState)

	first := NewTracerGeneration()
	first.SetServiceName("first", OriginCode, ProductTracer)
	require.NoError(t, publishGenerationForTest(first, first.PrepareClaims()))

	second := NewTracerGeneration()
	second.SetServiceName("second", OriginCode, ProductTracer)
	require.NoError(t, publishGenerationForTest(second, second.PrepareClaims()))

	rate := 0.25
	require.True(t, first.GlobalSampleRateConfig().HandleRC(&rate))
	assert.Equal(t, 0.25, first.GlobalSampleRate())
	assert.NotEqual(t, 0.25, second.GlobalSampleRate())
	assert.Equal(t, "first", first.ServiceName())
	assert.Same(t, second, Get())
}

func TestRetiredGenerationHeaderRemoteConfigCannotMutateActiveGlobalTags(t *testing.T) {
	resetGlobalState()
	t.Cleanup(resetGlobalState)
	globalconfig.ClearHeaderTags()
	t.Cleanup(globalconfig.ClearHeaderTags)

	first := NewTracerGeneration()
	first.SetHeaderAsTags([]string{"X-First:first.tag"}, OriginCode, ProductTracer)
	require.NoError(t, publishGenerationForTest(first, first.PrepareClaims()))
	assert.Equal(t, "first.tag", globalconfig.HeaderTag("X-First"))

	second := NewTracerGeneration()
	second.SetHeaderAsTags([]string{"X-Second:second.tag"}, OriginCode, ProductTracer)
	require.NoError(t, publishGenerationForTest(second, second.PrepareClaims()))
	assert.Equal(t, "second.tag", globalconfig.HeaderTag("X-Second"))
	assert.Empty(t, globalconfig.HeaderTag("X-First"))

	late := []string{"X-Retired:retired.tag"}
	require.True(t, first.HeaderAsTagsConfig().HandleRC(&late))
	assert.Equal(t, late, first.HeaderAsTags())
	assert.Equal(t, "second.tag", globalconfig.HeaderTag("X-Second"))
	assert.Empty(t, globalconfig.HeaderTag("X-Retired"))
}

func TestPublishRejectsPreparedClaimsForDifferentOrMutatedGeneration(t *testing.T) {
	resetGlobalState()
	t.Cleanup(resetGlobalState)

	first := NewTracerGeneration()
	first.SetServiceName("first", OriginCode, ProductTracer)
	prepared := first.PrepareClaims()

	second := NewTracerGeneration()
	require.Error(t, publishGenerationForTest(second, prepared))

	first.SetEnv("changed-after-prepare", OriginCode, ProductTracer)
	require.Error(t, publishGenerationForTest(first, prepared))
	require.NotSame(t, first, Get())
}

func TestPublishRejectsPreparedClaimsFromReplacedStore(t *testing.T) {
	resetGlobalState()
	t.Cleanup(resetGlobalState)

	staged := NewTracerGeneration()
	staged.SetServiceName("old-store", OriginCode, ProductTracer)
	prepared := staged.PrepareClaims()

	resetGlobalState()
	current := Get()
	require.Error(t, publishGenerationForTest(staged, prepared))
	assert.Same(t, current, Get())
}

func TestPublishRejectsClaimedFieldChangedWithoutUpdatingClaims(t *testing.T) {
	resetGlobalState()
	t.Cleanup(resetGlobalState)

	staged := NewTracerGeneration()
	staged.SetServiceName("prepared", OriginCode, ProductTracer)
	prepared := staged.PrepareClaims()
	staged.SetServiceName("calculated-after-prepare", OriginCalculated)

	require.Error(t, publishGenerationForTest(staged, prepared))
	require.NotSame(t, staged, Get())
}

func TestPublishPreparedClaimsIsSingleUseUnderConcurrency(t *testing.T) {
	resetGlobalState()
	t.Cleanup(resetGlobalState)

	staged := NewTracerGeneration()
	staged.SetServiceName("single-use", OriginCode, ProductTracer)
	prepared := staged.PrepareClaims()

	start := make(chan struct{})
	results := make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			results <- publishGenerationForTest(staged, prepared)
		}()
	}
	close(start)
	first, second := <-results, <-results
	assert.NotEqual(t, first == nil, second == nil, "exactly one publication must succeed")
	assert.Same(t, staged, Get())
	assert.False(t, staged.retired.Load(), "a duplicate publish must not retire the current generation")
}

func TestAcquireProductClaimsSnapshotsMutableValues(t *testing.T) {
	resetGlobalState()
	t.Cleanup(resetGlobalState)

	value := []string{"one:1", "two:2"}
	release, accepted := AcquireProductClaims(ProductProfiler, []Claim{{
		Name: "DD_TAGS", Value: value,
	}})
	defer release()
	require.True(t, accepted["DD_TAGS"])
	value[0] = "mutated:1"

	releaseSame, accepted := AcquireProductClaims(ProductProfiler, []Claim{{
		Name: "DD_TAGS", Value: []string{"one:1", "two:2"},
	}})
	defer releaseSame()
	assert.True(t, accepted["DD_TAGS"], "caller mutation must not change the stored claim")
}

func TestTagClaimsUseCanonicalProgrammaticRepresentation(t *testing.T) {
	resetGlobalState()
	t.Cleanup(resetGlobalState)

	release, accepted := AcquireProductClaims(ProductProfiler, []Claim{{
		Name: "DD_TAGS",
		Value: map[string]any{
			"team":    "apm",
			"complex": `a:b,c\\d`,
		},
	}})
	t.Cleanup(release)
	require.True(t, accepted["DD_TAGS"])

	sameRelease, accepted := AcquireProductClaims(ProductProfiler, []Claim{{
		Name: "DD_TAGS",
		Value: []string{
			"team:discarded-duplicate",
			`complex:a:b,c\\d`,
			"team:apm",
		},
	}})
	t.Cleanup(sameRelease)
	assert.True(t, accepted["DD_TAGS"], "map and slice inputs must normalize ordering, duplicates, and delimiters identically")
}

func TestTagClaimsExcludeSourceAndCalculatedTags(t *testing.T) {
	resetGlobalState()
	t.Cleanup(resetGlobalState)
	t.Setenv("DD_TAGS", "source:environment")

	release, accepted := AcquireProductClaims(ProductProfiler, []Claim{{
		Name: "DD_TAGS", Value: []string{"programmatic:value"},
	}})
	t.Cleanup(release)
	require.True(t, accepted["DD_TAGS"])

	staged := NewTracerGeneration()
	staged.SetGlobalTag("programmatic", "value", OriginCode, ProductTracer)
	staged.SetGlobalTag("_dd.runtime_id", "calculated", OriginCalculated)
	require.NoError(t, publishGenerationForTest(staged, staged.PrepareClaims()))
	assert.Equal(t, "environment", staged.GlobalTags()["source"])
	assert.Equal(t, "calculated", staged.GlobalTags()["_dd.runtime_id"])
}

func TestTagClaimsRejectExcessiveDepthWithoutRetainingInput(t *testing.T) {
	resetGlobalState()
	t.Cleanup(resetGlobalState)

	var deep any = "leaf"
	for range 128 {
		deep = map[string]any{"nested": deep}
	}
	release, accepted := AcquireProductClaims(ProductProfiler, []Claim{{
		Name: "DD_TAGS", Value: deep,
	}})
	release()
	assert.False(t, accepted["DD_TAGS"])

	staged := NewTracerGeneration()
	staged.SetGlobalTag("deep", deep, OriginCode, ProductTracer)
	prepared := staged.PrepareClaims()
	assert.IsType(t, unsupportedClaimValue{}, prepared.claims["DD_TAGS"])
}

func TestTagClaimsRejectExcessiveNodeCount(t *testing.T) {
	resetGlobalState()
	t.Cleanup(resetGlobalState)

	tags := make(map[string]string, 1025)
	for i := range 1025 {
		tags[fmt.Sprintf("tag-%04d", i)] = "value"
	}
	release, accepted := AcquireProductClaims(ProductProfiler, []Claim{{
		Name: "DD_TAGS", Value: tags,
	}})
	release()
	assert.False(t, accepted["DD_TAGS"])
}

func TestTagClaimsRejectCyclesWithoutRetainingInput(t *testing.T) {
	resetGlobalState()
	t.Cleanup(resetGlobalState)

	cyclic := map[string]any{}
	cyclic["self"] = cyclic
	staged := NewTracerGeneration()
	staged.SetGlobalTag("cycle", cyclic, OriginCode, ProductTracer)
	cyclic["mutated-after-call"] = "must-not-be-retained"

	prepared := staged.PrepareClaims()
	assert.IsType(t, unsupportedClaimValue{}, prepared.claims["DD_TAGS"])
	assert.IsType(t, unsupportedClaimValue{}, staged.GlobalTags()["cycle"])
}

func TestAgentURLClaimsAreCanonicalCredentialSensitiveFingerprints(t *testing.T) {
	resetGlobalState()
	t.Cleanup(resetGlobalState)

	raw := "https://alice:secret@example.test:443"
	parsed, err := url.Parse(raw)
	require.NoError(t, err)
	release, accepted := AcquireProductClaims(ProductProfiler, []Claim{{
		Name: "DD_TRACE_AGENT_URL", Value: parsed,
	}})
	t.Cleanup(release)
	require.True(t, accepted["DD_TRACE_AGENT_URL"])

	sameRelease, accepted := AcquireProductClaims(ProductProfiler, []Claim{{
		Name: "DD_TRACE_AGENT_URL", Value: parsed.String(),
	}})
	t.Cleanup(sameRelease)
	assert.True(t, accepted["DD_TRACE_AGENT_URL"])

	differentRelease, accepted := AcquireProductClaims(ProductProfiler, []Claim{{
		Name: "DD_TRACE_AGENT_URL", Value: "https://alice:different@example.test:443",
	}})
	t.Cleanup(differentRelease)
	assert.False(t, accepted["DD_TRACE_AGENT_URL"], "credential changes must remain claim conflicts")

	staged := NewTracerGeneration()
	staged.SetAgentURL(parsed, OriginCode, ProductTracer)
	prepared := staged.PrepareClaims()
	require.NotContains(t, fmt.Sprintf("%#v", prepared.claims), "secret")
	globalStore.mu.Lock()
	active := fmt.Sprintf("%#v", globalStore.claims["DD_TRACE_AGENT_URL"])
	globalStore.mu.Unlock()
	require.NotContains(t, active, "secret")
}

func TestSetAgentURLClonesCallerValue(t *testing.T) {
	resetGlobalState()
	t.Cleanup(resetGlobalState)

	staged := NewTracerGeneration()
	caller, err := url.Parse("https://alice:secret@example.test:443")
	require.NoError(t, err)
	staged.SetAgentURL(caller, OriginCode, ProductTracer)
	caller.Host = "mutated.example.test"
	caller.User = url.UserPassword("mallory", "changed")

	assert.Equal(t, "example.test:443", staged.RawAgentURL().Host)
	assert.Equal(t, "alice", staged.RawAgentURL().User.Username())
}

func TestPublishedGenerationRejectsTracerSetters(t *testing.T) {
	resetGlobalState()
	t.Cleanup(resetGlobalState)

	staged := NewTracerGeneration()
	staged.SetServiceName("published", OriginCode, ProductTracer)
	staged.SetGlobalTag("before", "published", OriginCode, ProductTracer)
	require.NoError(t, publishGenerationForTest(staged, staged.PrepareClaims()))

	staged.SetServiceName("late", OriginCode, ProductTracer)
	staged.SetGlobalTag("late", "must-not-apply", OriginCode, ProductTracer)
	assert.Equal(t, "published", staged.ServiceName())
	assert.NotContains(t, staged.GlobalTags(), "late")
}

func TestTracerSetterRacingPublicationIsTransactional(t *testing.T) {
	for range 64 {
		resetGlobalState()
		staged := NewTracerGeneration()
		staged.SetServiceName("prepared", OriginCode, ProductTracer)
		prepared := staged.PrepareClaims()

		start := make(chan struct{})
		var (
			publishErr error
			wg         sync.WaitGroup
		)
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			publishErr = publishGenerationForTest(staged, prepared)
		}()
		go func() {
			defer wg.Done()
			<-start
			staged.SetServiceName("racing-setter", OriginCode, ProductTracer)
		}()
		close(start)
		wg.Wait()

		if publishErr == nil {
			assert.Equal(t, "prepared", staged.ServiceName())
		} else {
			assert.Equal(t, "racing-setter", staged.ServiceName())
		}
	}
	resetGlobalState()
}

func TestRevertedTracerClaimDropsStaleOverrideBookkeeping(t *testing.T) {
	resetGlobalState()
	t.Cleanup(resetGlobalState)
	t.Setenv("DD_SERVICE", "source")

	release, accepted := AcquireProductClaims(ProductProfiler, []Claim{{
		Name: "DD_SERVICE", Value: "profiler",
	}})
	t.Cleanup(release)
	require.True(t, accepted["DD_SERVICE"])

	staged := NewTracerGeneration()
	staged.SetServiceName("tracer", OriginCode, ProductTracer)
	staged.PrepareClaims()
	require.Equal(t, "source", staged.ServiceName())

	staged.SetServiceName("profiler", OriginCode, ProductProfiler)
	assert.Equal(t, "profiler", staged.ServiceName())
}

func TestClaimProgrammaticTagsExcludeCalculatedTags(t *testing.T) {
	resetGlobalState()
	t.Cleanup(resetGlobalState)

	release, accepted := AcquireProductClaims(ProductProfiler, []Claim{{
		Name: "DD_TAGS", Value: map[string]any{"team": "apm"},
	}})
	defer release()
	require.True(t, accepted["DD_TAGS"])

	staged := NewTracerGeneration()
	staged.SetGlobalTag("team", "apm", OriginCode, ProductTracer)
	prepared := staged.PrepareClaims()
	staged.SetGlobalTag("_dd.runtime_id", "calculated", OriginCalculated)

	require.NoError(t, publishGenerationForTest(staged, prepared))
	assert.Equal(t, "calculated", staged.GlobalTags()["_dd.runtime_id"])
}

func TestAcquireProductClaimsRejectsUnboundedOrMutableState(t *testing.T) {
	resetGlobalState()
	t.Cleanup(resetGlobalState)

	type callbackBearingValue struct {
		Value string
	}
	for i := range 1000 {
		name := "DD_UNREGISTERED_" + string(rune(i))
		release, accepted := AcquireProductClaims(ProductProfiler, []Claim{{
			Name: name, Value: "value",
		}})
		release()
		assert.False(t, accepted[name])
	}
	release, accepted := AcquireProductClaims(ProductProfiler, []Claim{{
		Name: "DD_TAGS", Value: &callbackBearingValue{Value: "mutable"},
	}})
	release()
	assert.False(t, accepted["DD_TAGS"])

	release, accepted = AcquireProductClaims(ProductTracer, []Claim{{
		Name: "DD_SERVICE", Value: "must-use-staged-publication",
	}})
	release()
	assert.False(t, accepted["DD_SERVICE"])

	globalStore.mu.Lock()
	assert.Empty(t, globalStore.claims)
	globalStore.mu.Unlock()
}
