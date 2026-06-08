// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gotesting

import (
	"flag"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/internal/bazel"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations/gotesting/coverage"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils/net"
)

const (
	itrBackfillReasonUnavailable        = "skippable response unavailable"
	itrBackfillReasonMissingCoverage    = "backend coverage metadata missing"
	itrBackfillReasonUnsafeCoverage     = "backend coverage metadata unsafe"
	itrBackfillReasonRuntimeUnavailable = "runtime coverage unavailable"
	itrBackfillReasonUnsupportedMode    = "coverage mode unsupported"
	itrBackfillReasonNarrowingFlags     = "narrowing test flags present"
	itrBackfillReasonBazelUnsupported   = "bazel coverage mode unsupported"
	itrBackfillReasonResponseScope      = "skippable response outside current test process"
	itrBackfillReasonParameterized      = "skippable response has unsupported test parameters"
)

var activeITRState atomic.Pointer[itrState]

type itrState struct {
	settings              *net.SettingsResponseData
	response              *net.SkippableTestsResponse
	coverageActive        bool
	coverageBackfillReady bool
	disabledReason        string
	actualSkips           atomic.Uint64
}

type itrSkipDecision struct {
	skip      bool
	forcedRun bool
}

func newITRState(settings *net.SettingsResponseData) *itrState {
	state := &itrState{settings: settings}
	if settings == nil || !settings.ItrEnabled || !settings.TestsSkipping {
		activeITRState.Store(state)
		return state
	}

	state.coverageActive = testing.CoverMode() != ""
	state.response = integrations.GetSkippableTestsResponse()
	if !state.coverageActive {
		activeITRState.Store(state)
		return state
	}

	switch {
	case bazel.IsManifestModeEnabled() || bazel.IsPayloadFilesModeEnabled():
		state.disabledReason = itrBackfillReasonBazelUnsupported
	case hasNarrowingTestFlags():
		state.disabledReason = itrBackfillReasonNarrowingFlags
	case !coverage.CanComputeCoverageProfile():
		state.disabledReason = itrBackfillReasonRuntimeUnavailable
	case !coverage.CanCollect():
		state.disabledReason = itrBackfillReasonUnsupportedMode
	case state.response == nil:
		state.disabledReason = itrBackfillReasonUnavailable
	case !state.response.CoveragePresent:
		state.disabledReason = itrBackfillReasonMissingCoverage
	case !state.response.CoverageBackfillSafe:
		state.disabledReason = state.response.CoverageBackfillReason
		if state.disabledReason == "" {
			state.disabledReason = itrBackfillReasonUnsafeCoverage
		}
	default:
		preflight := coverage.PreflightBackfill(coverage.BackfillInput{
			BackendCoverage: state.response.Coverage,
		})
		if preflight.Reason != "" {
			state.disabledReason = preflight.Reason
		} else {
			state.coverageBackfillReady = true
		}
	}

	activeITRState.Store(state)
	return state
}

func currentITRState() *itrState {
	if state := activeITRState.Load(); state != nil {
		return state
	}
	state := &itrState{}
	activeITRState.Store(state)
	return state
}

func (s *itrState) testsSkippingEnabled() bool {
	if s == nil || s.settings == nil {
		return false
	}
	if !s.settings.ItrEnabled || !s.settings.TestsSkipping {
		return false
	}
	return true
}

func (s *itrState) hasSkippableTests() bool {
	return s != nil && s.response != nil && len(s.response.Skippables) > 0
}

// validateCoverageBackfillScope keeps response-level aggregate coverage tied to
// the current test binary. If the skippable response contains candidates that
// cannot be executed by this process, its meta.coverage aggregate may include
// coverage from tests this process will never skip.
func (s *itrState) validateCoverageBackfillScope(testInfos []*testingTInfo) {
	if s == nil || !s.coverageActive || !s.coverageBackfillReady || s.response == nil || len(s.response.Skippables) == 0 {
		return
	}

	localTests := localTestLookup(testInfos)
	for suiteName, tests := range s.response.Skippables {
		for testName, candidates := range tests {
			if localTests[suiteName][testName] {
				for _, candidate := range candidates {
					if strings.TrimSpace(candidate.Parameters) != "" {
						s.disableCoverageBackfill(itrBackfillReasonParameterized)
						return
					}
				}
				continue
			}
			s.disableCoverageBackfill(itrBackfillReasonResponseScope)
			return
		}
	}
}

// localTestLookup indexes the top-level tests known to the current testing.M.
func localTestLookup(testInfos []*testingTInfo) map[string]map[string]bool {
	localTests := make(map[string]map[string]bool, len(testInfos))
	for _, testInfo := range testInfos {
		if testInfo == nil {
			continue
		}
		testsByName, ok := localTests[testInfo.suiteName]
		if !ok {
			testsByName = map[string]bool{}
			localTests[testInfo.suiteName] = testsByName
		}
		testsByName[testInfo.testName] = true
	}
	return localTests
}

// disableCoverageBackfill prevents aggregate backend coverage from being applied
// while preserving the normal ITR skip decision.
func (s *itrState) disableCoverageBackfill(reason string) {
	s.coverageBackfillReady = false
	s.disabledReason = reason
}

func (s *itrState) decisionFor(testInfo *testingTInfo, execMeta *testExecutionMetadata, isUnskippable bool) itrSkipDecision {
	if s == nil || s.settings == nil || !s.settings.ItrEnabled || !s.settings.TestsSkipping {
		return itrSkipDecision{}
	}

	candidates := s.skippableCandidates(testInfo)
	if len(candidates) == 0 {
		return itrSkipDecision{}
	}

	if !s.testsSkippingEnabled() || execMeta.isAttemptToFix || execMeta.isAModifiedTest {
		return itrSkipDecision{}
	}

	if isUnskippable {
		return itrSkipDecision{forcedRun: true}
	}

	if s.coverageActive {
		for _, candidate := range candidates {
			if candidate.MissingLineCodeCoverage {
				return itrSkipDecision{}
			}
		}
	}

	return itrSkipDecision{skip: true}
}

func (s *itrState) skippableCandidates(testInfo *testingTInfo) []net.SkippableResponseDataAttributes {
	if s == nil || s.response == nil || s.response.Skippables == nil {
		return nil
	}
	suiteMap, ok := s.response.Skippables[testInfo.suiteName]
	if !ok {
		return nil
	}
	candidates := suiteMap[testInfo.testName]
	matching := make([]net.SkippableResponseDataAttributes, 0, len(candidates))
	for _, candidate := range candidates {
		// Java includes parameters in the test identifier. Go tests do not have
		// a parameter identifier here, so parameterized candidates must not match
		// the non-parameterized top-level test.
		if strings.TrimSpace(candidate.Parameters) != "" {
			continue
		}
		matching = append(matching, candidate)
	}
	return matching
}

func (s *itrState) markActualSkip() uint64 {
	if s == nil {
		return 0
	}
	return s.actualSkips.Add(1)
}

func (s *itrState) actualSkipCount() int {
	if s == nil {
		return 0
	}
	return int(s.actualSkips.Load())
}

func finalizeITRCoverageBackfill() (float64, bool, bool) {
	state := currentITRState()
	if !state.coverageActive || !state.coverageBackfillReady || state.response == nil {
		return 0, false, true
	}

	coverage.ConfigureBackfill(coverage.BackfillInput{
		BackendCoverage: state.response.Coverage,
		ActualSkips:     state.actualSkipCount(),
	})
	result := coverage.FinalizeBackfill()
	if result.Reason != "" {
		return 0, false, state.actualSkipCount() == 0
	}
	return result.Coverage, true, true
}

func hasNarrowingTestFlags() bool {
	for _, name := range narrowingTestFlagNames {
		if testFlagSetFromArgs(name) || testFlagSetFromFlagVisit(name) {
			return true
		}
	}
	return false
}

var narrowingTestFlagNames = []string{
	"test.run",
	"test.skip",
	"test.list",
	"test.fuzz",
	"test.bench",
	"test.short",
}

func testFlagSetFromArgs(name string) bool {
	shortName := strings.TrimPrefix(name, "test.")
	for _, arg := range os.Args[1:] {
		if arg == "--" {
			break
		}
		if flagArgNarrows(arg, name) || flagArgNarrows(arg, shortName) {
			return true
		}
	}
	return false
}

func flagArgNarrows(arg, name string) bool {
	for _, prefix := range []string{"-" + name, "--" + name} {
		if arg == prefix {
			return flagValueNarrows(name, "", false)
		}
		if value, ok := strings.CutPrefix(arg, prefix+"="); ok {
			return flagValueNarrows(name, value, true)
		}
	}
	return false
}

func flagValueNarrows(name, value string, hasValue bool) bool {
	if strings.TrimPrefix(name, "test.") != "short" {
		return true
	}
	if !hasValue {
		return true
	}
	parsed, err := strconv.ParseBool(value)
	return err != nil || parsed
}

func testFlagSetFromFlagVisit(name string) bool {
	found := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == name && f.Value.String() != defaultTestFlagValue(name) {
			found = true
		}
	})
	return found
}

func defaultTestFlagValue(name string) string {
	switch name {
	case "test.short":
		return strconv.FormatBool(false)
	default:
		return ""
	}
}
