package subtests

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"sort"
	"strconv"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations"
	gotesting "github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations/gotesting"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils/net"
)

const (
	moduleUnderTest   = "github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations/gotesting/subtests"
	suiteUnderTest    = "fixtures_test.go"
	parentTestName    = "TestSubtestManagement"
	parallelToggleEnv = "SUBTEST_MATRIX_PARALLEL"
)

var (
	availableScenarios = []*matrixScenario{
		baselineScenario(),
		subDisabledScenario(),
		subQuarantinedScenario(),
		parentQuarantinedScenario(),
		parentQuarantinedAttemptFixScenario(),
		parentAttemptFixScenario(),
		subAttemptFixOnlyScenario(),
		subAttemptFixCustomRetriesScenario(),
		subAttemptFixParallelScenario(),
		parentAndSubAttemptFixScenario(),
	}
	scenarioByName = func() map[string]*matrixScenario {
		m := make(map[string]*matrixScenario, len(availableScenarios))
		for _, sc := range availableScenarios {
			m[sc.name] = sc
		}
		return m
	}()
)

type directive struct {
	disabled     bool
	quarantined  bool
	attemptToFix bool
}

type matrixScenario struct {
	name      string
	configure func(*scenarioContext)
	validate  func([]*mocktracer.Span)
}

type scenarioContext struct {
	data                *net.TestManagementTestsResponseDataModules
	attemptToFixRetries int
	env                 map[string]string
}

// newScenarioContext prepares an empty scenario scaffold with storage for module directives.
func newScenarioContext() *scenarioContext {
	return &scenarioContext{
		data: &net.TestManagementTestsResponseDataModules{
			Modules: make(map[string]net.TestManagementTestsResponseDataSuites),
		},
		attemptToFixRetries: 0,
		env:                 make(map[string]string),
	}
}

// setParentDirective records backend directives for the parent test in the scenario payload.
func (ctx *scenarioContext) setParentDirective(dir directive) {
	module := ctx.data.Modules[moduleUnderTest]
	// Lazily allocate the suites map so directives can be written.
	if module.Suites == nil {
		module.Suites = make(map[string]net.TestManagementTestsResponseDataTests)
	}
	suite := module.Suites[suiteUnderTest]
	// Lazily allocate the test map for the suite.
	if suite.Tests == nil {
		suite.Tests = make(map[string]net.TestManagementTestsResponseDataTestProperties)
	}
	props := net.TestManagementTestsResponseDataTestPropertiesAttributes{
		Disabled:     dir.disabled,
		Quarantined:  dir.quarantined,
		AttemptToFix: dir.attemptToFix,
	}
	suite.Tests[parentTestName] = net.TestManagementTestsResponseDataTestProperties{Properties: props}
	module.Suites[suiteUnderTest] = suite
	ctx.data.Modules[moduleUnderTest] = module
}

// setSubDirective associates directives with a specific subtest within the scenario payload.
func (ctx *scenarioContext) setSubDirective(subName string, dir directive) {
	module := ctx.data.Modules[moduleUnderTest]
	// Ensure the suite map exists before inserting the subtest directive.
	if module.Suites == nil {
		module.Suites = make(map[string]net.TestManagementTestsResponseDataTests)
	}
	suite := module.Suites[suiteUnderTest]
	// Initialise per-test properties map when missing.
	if suite.Tests == nil {
		suite.Tests = make(map[string]net.TestManagementTestsResponseDataTestProperties)
	}
	fullName := fmt.Sprintf("%s/%s", parentTestName, subName)
	props := net.TestManagementTestsResponseDataTestPropertiesAttributes{
		Disabled:     dir.disabled,
		Quarantined:  dir.quarantined,
		AttemptToFix: dir.attemptToFix,
	}
	suite.Tests[fullName] = net.TestManagementTestsResponseDataTestProperties{Properties: props}
	module.Suites[suiteUnderTest] = suite
	ctx.data.Modules[moduleUnderTest] = module
}

// ensureSuite creates suite entries in the mock payload when missing so directives can be attached.
func (ctx *scenarioContext) ensureSuite() {
	module := ctx.data.Modules[moduleUnderTest]
	// Avoid nil maps so later writes succeed.
	if module.Suites == nil {
		module.Suites = make(map[string]net.TestManagementTestsResponseDataTests)
	}
	suite := module.Suites[suiteUnderTest]
	// Guarantee there is at least an empty tests map for the suite.
	if suite.Tests == nil {
		suite.Tests = make(map[string]net.TestManagementTestsResponseDataTestProperties)
	}
	module.Suites[suiteUnderTest] = suite
	ctx.data.Modules[moduleUnderTest] = module
}

// setEnv records an environment variable override to be applied during scenario execution.
func (ctx *scenarioContext) setEnv(key, value string) {
	if ctx.env == nil {
		ctx.env = make(map[string]string)
	}
	ctx.env[key] = value
}

// baselineScenario captures the control case where no directives are present, ensuring
// that the harness keeps its default behaviour without subtest-specific spans.
func baselineScenario() *matrixScenario {
	return &matrixScenario{
		name: "baseline",
		configure: func(ctx *scenarioContext) {
			// Ensure the suite exists so subsequent lookups succeed.
			ctx.ensureSuite()
			// Explicitly reset the attempt-to-fix retry budget for the identity scenario.
			ctx.attemptToFixRetries = 0
			module, suite := utils.GetModuleAndSuiteName(reflect.ValueOf(TestSubtestManagement).Pointer())
			debugMatrixf("baseline identity module=%s suite=%s", module, suite)
		},
		validate: func(spans []*mocktracer.Span) {
			testSpans := filterTestSpans(spans)
			debugMatrixf("baseline captured %d test spans", len(testSpans))
			for _, span := range testSpans {
				// Skip nil spans because they do not carry any metadata.
				if span == nil {
					continue
				}
				// Log each resource to help diagnose unexpected spans during debugging.
				if resource, ok := span.Tag(ext.ResourceName).(string); ok {
					debugMatrixf(" - resource: %s", resource)
				}
			}

			parentResource := fmt.Sprintf("%s.%s", suiteUnderTest, parentTestName)
			parentSpans := spansByResource(testSpans, parentResource)
			requireSpanCount(parentSpans, 1, "parent baseline")

			assertTagEquals(parentSpans[0], constants.TestStatus, constants.TestStatusPass, "parent baseline status")
			assertTagNotTrue(parentSpans[0], constants.TestIsDisabled, "parent baseline disabled")
			assertTagNotTrue(parentSpans[0], constants.TestIsQuarantined, "parent baseline quarantined")
			assertTagNotTrue(parentSpans[0], constants.TestIsAttempToFix, "parent baseline attempt_to_fix")

			for _, sub := range []string{"SubDisabled", "SubQuarantined", "SubAttemptFix", "SubAttemptFixParallel"} {
				resource := fmt.Sprintf("%s/%s", parentResource, sub)
				subSpans := spansByResource(testSpans, resource)
				requireSpanCount(subSpans, 0, fmt.Sprintf("subtest %s baseline count", sub))
			}
		},
	}
}

// subAttemptFixOnlyScenario verifies that only the child subtest orchestrates attempt-to-fix retries
// while the parent remains neutral.
func subAttemptFixOnlyScenario() *matrixScenario {
	return &matrixScenario{
		name: "sub_attempt_to_fix_only",
		configure: func(ctx *scenarioContext) {
			// Initialise the suite and make the retry budget available to the subtest.
			ctx.ensureSuite()
			ctx.attemptToFixRetries = 3
			ctx.setSubDirective("SubAttemptFix", directive{attemptToFix: true})
			module, suite := utils.GetModuleAndSuiteName(reflect.ValueOf(TestSubtestManagement).Pointer())
			debugMatrixf("sub_attempt_to_fix_only identity module=%s suite=%s", module, suite)
		},
		validate: func(spans []*mocktracer.Span) {
			testSpans := filterTestSpans(spans)
			debugMatrixf("sub_attempt_to_fix_only captured %d test spans", len(testSpans))
			for _, span := range testSpans {
				// Guard against nil entries to avoid panics when introspecting tags.
				if span == nil {
					continue
				}
				// Provide verbose details when debugging span resources.
				if resource, ok := span.Tag(ext.ResourceName).(string); ok {
					debugMatrixf(" - resource: %s", resource)
				}
			}

			parentResource := fmt.Sprintf("%s.%s", suiteUnderTest, parentTestName)
			parentSpans := spansByResource(testSpans, parentResource)
			requireSpanCount(parentSpans, 1, "sub attempt-to-fix-only parent count")
			assertTagNotTrue(parentSpans[0], constants.TestIsAttempToFix, "sub attempt-to-fix-only parent tag")

			subResource := fmt.Sprintf("%s/%s", parentResource, "SubAttemptFix")
			subSpans := spansByResource(testSpans, subResource)
			requireSpanCount(subSpans, 3, "sub attempt-to-fix-only span count")
			sort.Slice(subSpans, func(i, j int) bool {
				// Order spans chronologically so comments and assertions match the retry lifecycle.
				return subSpans[i].StartTime().Before(subSpans[j].StartTime())
			})
			for idx, span := range subSpans {
				assertTagEquals(span, constants.TestIsAttempToFix, "true", fmt.Sprintf("sub attempt-to-fix-only tag span %d", idx))
			}
			lastSpan := subSpans[len(subSpans)-1]
			assertTagEquals(lastSpan, constants.TestAttemptToFixPassed, "true", "sub attempt-to-fix-only success")
			assertTagEquals(lastSpan, constants.TestStatus, constants.TestStatusPass, "sub attempt-to-fix-only final status")
			assertTagCount(subSpans, constants.TestIsRetry, "true", 2, "sub attempt-to-fix-only retry tag count")
			assertTagCount(subSpans, constants.TestRetryReason, constants.AttemptToFixRetryReason, 2, "sub attempt-to-fix-only retry reason count")
		},
	}
}

// subAttemptFixCustomRetriesScenario demonstrates that a child can request a larger retry
// budget without involving the parent, ensuring the additional attempts are tagged correctly.
func subAttemptFixCustomRetriesScenario() *matrixScenario {
	return &matrixScenario{
		name: "sub_attempt_to_fix_custom_retries",
		configure: func(ctx *scenarioContext) {
			// Initialise suite storage so directives can be attached safely.
			ctx.ensureSuite()
			ctx.attemptToFixRetries = 5
			ctx.setSubDirective("SubAttemptFix", directive{attemptToFix: true})
		},
		validate: func(spans []*mocktracer.Span) {
			testSpans := filterTestSpans(spans)

			parentResource := fmt.Sprintf("%s.%s", suiteUnderTest, parentTestName)
			parentSpans := spansByResource(testSpans, parentResource)
			requireSpanCount(parentSpans, 1, "sub attempt-to-fix custom parent count")
			assertTagNotTrue(parentSpans[0], constants.TestIsAttempToFix, "sub attempt-to-fix custom parent tag")

			subResource := fmt.Sprintf("%s/%s", parentResource, "SubAttemptFix")
			subSpans := spansByResource(testSpans, subResource)
			requireSpanCount(subSpans, 5, "sub attempt-to-fix custom child span count")
			sort.Slice(subSpans, func(i, j int) bool {
				// Sort to make reasoning about retries deterministic.
				return subSpans[i].StartTime().Before(subSpans[j].StartTime())
			})
			for idx, span := range subSpans {
				// Each retry should still carry the attempt-to-fix tag for visibility.
				assertTagEquals(span, constants.TestIsAttempToFix, "true", fmt.Sprintf("sub attempt-to-fix custom tag span %d", idx))
			}
			subFinal := subSpans[len(subSpans)-1]
			assertTagEquals(subFinal, constants.TestAttemptToFixPassed, "true", "sub attempt-to-fix custom success")
			assertTagEquals(subFinal, constants.TestStatus, constants.TestStatusPass, "sub attempt-to-fix custom final status")
			assertTagCount(subSpans, constants.TestIsRetry, "true", 4, "sub attempt-to-fix custom retry tag count")
			assertTagCount(subSpans, constants.TestRetryReason, constants.AttemptToFixRetryReason, 4, "sub attempt-to-fix custom retry reason count")
		},
	}
}

// subAttemptFixParallelScenario asserts that parallel subtests inherit attempt-to-fix behaviour
// without conflicting with the sequential sibling.
func subAttemptFixParallelScenario() *matrixScenario {
	return &matrixScenario{
		name: "sub_attempt_to_fix_parallel",
		configure: func(ctx *scenarioContext) {
			// Prepare suite metadata before writing directives.
			ctx.ensureSuite()
			ctx.attemptToFixRetries = 3
			ctx.setSubDirective("SubAttemptFix", directive{attemptToFix: true})
			ctx.setSubDirective("SubAttemptFixParallel", directive{attemptToFix: true})
			ctx.setEnv(parallelToggleEnv, "1")
		},
		validate: func(spans []*mocktracer.Span) {
			testSpans := filterTestSpans(spans)

			parentResource := fmt.Sprintf("%s.%s", suiteUnderTest, parentTestName)
			parentSpans := spansByResource(testSpans, parentResource)
			requireSpanCount(parentSpans, 1, "sub attempt-to-fix parallel parent count")
			assertTagNotTrue(parentSpans[0], constants.TestIsAttempToFix, "sub attempt-to-fix parallel parent tag")

			checkParallelChild := func(child string) {
				// Focus validations on a single subtest resource at a time.
				resource := fmt.Sprintf("%s/%s", parentResource, child)
				childSpans := spansByResource(testSpans, resource)
				requireSpanCount(childSpans, 3, fmt.Sprintf("%s attempt-to-fix parallel span count", child))
				sort.Slice(childSpans, func(i, j int) bool {
					// Sort to match retry order regardless of goroutine scheduling.
					return childSpans[i].StartTime().Before(childSpans[j].StartTime())
				})
				for idx, span := range childSpans {
					// Confirm each execution is correctly tagged as part of the attempt-to-fix flow.
					assertTagEquals(span, constants.TestIsAttempToFix, "true", fmt.Sprintf("%s attempt-to-fix parallel tag span %d", child, idx))
				}
				final := childSpans[len(childSpans)-1]
				assertTagEquals(final, constants.TestAttemptToFixPassed, "true", fmt.Sprintf("%s attempt-to-fix parallel success", child))
				assertTagEquals(final, constants.TestStatus, constants.TestStatusPass, fmt.Sprintf("%s attempt-to-fix parallel status", child))
				assertTagCount(childSpans, constants.TestIsRetry, "true", 2, fmt.Sprintf("%s attempt-to-fix parallel retry tag count", child))
				assertTagCount(childSpans, constants.TestRetryReason, constants.AttemptToFixRetryReason, 2, fmt.Sprintf("%s attempt-to-fix parallel retry reason count", child))
			}

			checkParallelChild("SubAttemptFix")
			checkParallelChild("SubAttemptFixParallel")
		},
	}
}

// subDisabledScenario asserts that a subtest disabled directive skips the child while
// leaving the parent untouched.
func subDisabledScenario() *matrixScenario {
	return &matrixScenario{
		name: "sub_disabled",
		configure: func(ctx *scenarioContext) {
			ctx.ensureSuite()
			ctx.setParentDirective(directive{})
			ctx.setSubDirective("SubDisabled", directive{disabled: true})
		},
		validate: func(spans []*mocktracer.Span) {
			testSpans := filterTestSpans(spans)

			parentResource := fmt.Sprintf("%s.%s", suiteUnderTest, parentTestName)
			parentSpans := spansByResource(testSpans, parentResource)
			requireSpanCount(parentSpans, 1, "sub disabled parent count")
			assertTagNotTrue(parentSpans[0], constants.TestIsDisabled, "parent disabled tag")

			subResource := fmt.Sprintf("%s/%s", parentResource, "SubDisabled")
			subSpans := spansByResource(testSpans, subResource)
			requireSpanCount(subSpans, 1, "sub disabled span count")
			assertTagEquals(subSpans[0], constants.TestIsDisabled, "true", "sub disabled tag")
			assertTagEquals(subSpans[0], constants.TestStatus, constants.TestStatusSkip, "sub disabled status")
		},
	}
}

// subQuarantinedScenario ensures a child-only quarantine directive reports the subtest as quarantined.
func subQuarantinedScenario() *matrixScenario {
	return &matrixScenario{
		name: "sub_quarantined",
		configure: func(ctx *scenarioContext) {
			ctx.ensureSuite()
			ctx.setParentDirective(directive{})
			ctx.setSubDirective("SubQuarantined", directive{quarantined: true})
		},
		validate: func(spans []*mocktracer.Span) {
			testSpans := filterTestSpans(spans)

			parentResource := fmt.Sprintf("%s.%s", suiteUnderTest, parentTestName)
			parentSpans := spansByResource(testSpans, parentResource)
			requireSpanCount(parentSpans, 1, "sub quarantined parent count")
			assertTagNotTrue(parentSpans[0], constants.TestIsQuarantined, "parent quarantined tag")

			subResource := fmt.Sprintf("%s/%s", parentResource, "SubQuarantined")
			subSpans := spansByResource(testSpans, subResource)
			requireSpanCount(subSpans, 1, "sub quarantined span count")
			assertTagEquals(subSpans[0], constants.TestIsQuarantined, "true", "sub quarantined tag")
			assertTagEquals(subSpans[0], constants.TestStatus, constants.TestStatusPass, "sub quarantined status")
		},
	}
}

// parentQuarantinedScenario shows that a quarantined parent automatically propagates the tag to its child.
func parentQuarantinedScenario() *matrixScenario {
	return &matrixScenario{
		name: "parent_quarantined",
		configure: func(ctx *scenarioContext) {
			ctx.ensureSuite()
			ctx.setParentDirective(directive{quarantined: true})
		},
		validate: func(spans []*mocktracer.Span) {
			testSpans := filterTestSpans(spans)

			parentResource := fmt.Sprintf("%s.%s", suiteUnderTest, parentTestName)
			parentSpans := spansByResource(testSpans, parentResource)
			requireSpanCount(parentSpans, 1, "parent quarantined span count")
			assertTagEquals(parentSpans[0], constants.TestIsQuarantined, "true", "parent quarantined tag")
			assertTagEquals(parentSpans[0], constants.TestStatus, constants.TestStatusPass, "parent quarantined status")

			subResource := fmt.Sprintf("%s/%s", parentResource, "SubQuarantined")
			subSpans := spansByResource(testSpans, subResource)
			requireSpanCount(subSpans, 1, "parent quarantined child span count")
			assertTagEquals(subSpans[0], constants.TestIsQuarantined, "true", "parent quarantined child tag")
			assertTagEquals(subSpans[0], constants.TestStatus, constants.TestStatusPass, "parent quarantined child status")
		},
	}
}

// parentQuarantinedAttemptFixScenario validates that a quarantined parent orchestrating retries
// propagates quarantine tags to the child while keeping ownership of the attempt-to-fix lifecycle.
func parentQuarantinedAttemptFixScenario() *matrixScenario {
	return &matrixScenario{
		name: "parent_quarantined_attempt_to_fix",
		configure: func(ctx *scenarioContext) {
			ctx.ensureSuite()
			ctx.attemptToFixRetries = 3
			ctx.setParentDirective(directive{quarantined: true, attemptToFix: true})
			ctx.setSubDirective("SubAttemptFix", directive{attemptToFix: true, quarantined: true})
		},
		validate: func(spans []*mocktracer.Span) {
			testSpans := filterTestSpans(spans)

			parentResource := fmt.Sprintf("%s.%s", suiteUnderTest, parentTestName)
			parentSpans := spansByResource(testSpans, parentResource)
			requireSpanCount(parentSpans, 3, "parent quarantine attempt-to-fix span count")
			// Confirm every parent retry is both quarantined and marked as attempt-to-fix.
			for idx, span := range parentSpans {
				assertTagEquals(span, constants.TestIsQuarantined, "true", fmt.Sprintf("parent quarantine attempt-to-fix quarantined tag span %d", idx))
				assertTagEquals(span, constants.TestIsAttempToFix, "true", fmt.Sprintf("parent quarantine attempt-to-fix attempt tag span %d", idx))
			}
			parentFinal := parentSpans[len(parentSpans)-1]
			assertTagEquals(parentFinal, constants.TestAttemptToFixPassed, "true", "parent quarantine attempt-to-fix success")
			assertTagEquals(parentFinal, constants.TestStatus, constants.TestStatusPass, "parent quarantine attempt-to-fix status")
			assertTagCount(parentSpans, constants.TestIsRetry, "true", 2, "parent quarantine attempt-to-fix retry tag count")
			assertTagCount(parentSpans, constants.TestRetryReason, constants.AttemptToFixRetryReason, 2, "parent quarantine attempt-to-fix retry reason count")

			subResource := fmt.Sprintf("%s/%s", parentResource, "SubAttemptFix")
			subSpans := spansByResource(testSpans, subResource)
			requireSpanCount(subSpans, 3, "parent quarantine attempt-to-fix child span count")
			// Each child execution must inherit the quarantined + attempt-to-fix state.
			for idx, span := range subSpans {
				assertTagEquals(span, constants.TestIsQuarantined, "true", fmt.Sprintf("child quarantine attempt-to-fix quarantined tag span %d", idx))
				assertTagEquals(span, constants.TestIsAttempToFix, "true", fmt.Sprintf("child quarantine attempt-to-fix attempt tag span %d", idx))
			}
			childFinal := subSpans[len(subSpans)-1]
			assertTagNotTrue(childFinal, constants.TestAttemptToFixPassed, "child quarantine attempt-to-fix success tag ownership")
			assertTagEquals(childFinal, constants.TestStatus, constants.TestStatusPass, "child quarantine attempt-to-fix status")
			assertTagCount(subSpans, constants.TestIsRetry, "true", 0, "child quarantine attempt-to-fix retry tag count")
			assertTagCount(subSpans, constants.TestRetryReason, constants.AttemptToFixRetryReason, 0, "child quarantine attempt-to-fix retry reason count")
		},
	}
}

// parentAttemptFixScenario checks that a parent-level attempt-to-fix directive wraps both
// parent and child executions with consistent retry tagging.
func parentAttemptFixScenario() *matrixScenario {
	return &matrixScenario{
		name: "parent_attempt_to_fix",
		configure: func(ctx *scenarioContext) {
			ctx.ensureSuite()
			ctx.attemptToFixRetries = 3
			ctx.setParentDirective(directive{attemptToFix: true})
		},
		validate: func(spans []*mocktracer.Span) {
			testSpans := filterTestSpans(spans)

			parentResource := fmt.Sprintf("%s.%s", suiteUnderTest, parentTestName)
			parentSpans := spansByResource(testSpans, parentResource)
			requireSpanCount(parentSpans, 3, "parent attempt-to-fix span count")
			// Each parent span should reflect that attempt-to-fix logic is active.
			for idx, span := range parentSpans {
				assertTagEquals(span, constants.TestIsAttempToFix, "true", fmt.Sprintf("parent attempt-to-fix tag span %d", idx))
			}
			assertTagCount(parentSpans, constants.TestIsRetry, "true", 2, "parent attempt-to-fix retry tag count")
			assertTagCount(parentSpans, constants.TestRetryReason, constants.AttemptToFixRetryReason, 2, "parent attempt-to-fix retry reason count")

			subResource := fmt.Sprintf("%s/%s", parentResource, "SubAttemptFix")
			subSpans := spansByResource(testSpans, subResource)
			requireSpanCount(subSpans, 3, "sub attempt-to-fix inherited span count")
			// The child inherits the attempt-to-fix directive and should pass under retry pressure.
			for idx, span := range subSpans {
				assertTagEquals(span, constants.TestIsAttempToFix, "true", fmt.Sprintf("sub inherited attempt-to-fix tag span %d", idx))
				assertTagEquals(span, constants.TestStatus, constants.TestStatusPass, fmt.Sprintf("sub inherited attempt-to-fix status span %d", idx))
			}
			assertTagCount(subSpans, constants.TestIsRetry, "true", 2, "sub inherited attempt-to-fix retry tag count")
			assertTagCount(subSpans, constants.TestRetryReason, constants.AttemptToFixRetryReason, 2, "sub inherited attempt-to-fix retry reason count")
		},
	}
}

// parentAndSubAttemptFixScenario makes sure that when both parent and child request
// attempt-to-fix behaviour the parent retains ownership of success tagging.
func parentAndSubAttemptFixScenario() *matrixScenario {
	return &matrixScenario{
		name: "parent_and_sub_attempt_to_fix",
		configure: func(ctx *scenarioContext) {
			ctx.ensureSuite()
			ctx.attemptToFixRetries = 3
			ctx.setParentDirective(directive{attemptToFix: true})
			ctx.setSubDirective("SubAttemptFix", directive{attemptToFix: true})
		},
		validate: func(spans []*mocktracer.Span) {
			testSpans := filterTestSpans(spans)

			parentResource := fmt.Sprintf("%s.%s", suiteUnderTest, parentTestName)
			parentSpans := spansByResource(testSpans, parentResource)
			requireSpanCount(parentSpans, 3, "parent/sub attempt-to-fix parent span count")
			// Validate every parent execution reflects the attempt-to-fix directive.
			for idx, span := range parentSpans {
				assertTagEquals(span, constants.TestIsAttempToFix, "true", fmt.Sprintf("parent/sub attempt-to-fix parent tag span %d", idx))
			}
			parentFinal := parentSpans[len(parentSpans)-1]
			assertTagEquals(parentFinal, constants.TestAttemptToFixPassed, "true", "parent/sub attempt-to-fix parent success")
			assertTagEquals(parentFinal, constants.TestStatus, constants.TestStatusPass, "parent/sub attempt-to-fix parent final status")
			assertTagCount(parentSpans, constants.TestIsRetry, "true", 2, "parent/sub attempt-to-fix parent retry tag count")
			assertTagCount(parentSpans, constants.TestRetryReason, constants.AttemptToFixRetryReason, 2, "parent/sub attempt-to-fix parent retry reason count")

			subResource := fmt.Sprintf("%s/%s", parentResource, "SubAttemptFix")
			subSpans := spansByResource(testSpans, subResource)
			requireSpanCount(subSpans, 3, "parent/sub attempt-to-fix child span count")
			// Even though the child has its own directive, it should not claim retry success.
			for idx, span := range subSpans {
				assertTagEquals(span, constants.TestIsAttempToFix, "true", fmt.Sprintf("parent/sub attempt-to-fix child tag span %d", idx))
			}
			subFinal := subSpans[len(subSpans)-1]
			assertTagNotTrue(subFinal, constants.TestAttemptToFixPassed, "parent/sub attempt-to-fix child success tag ownership")
			assertTagEquals(subFinal, constants.TestStatus, constants.TestStatusPass, "parent/sub attempt-to-fix child final status")
			assertTagCount(subSpans, constants.TestIsRetry, "true", 0, "parent/sub attempt-to-fix child retry tag count")
			assertTagCount(subSpans, constants.TestRetryReason, constants.AttemptToFixRetryReason, 0, "parent/sub attempt-to-fix child retry reason count")
		},
	}
}

// filterTestSpans keeps only test spans so scenario assertions ignore suite/module noise.
func filterTestSpans(spans []*mocktracer.Span) []*mocktracer.Span {
	var out []*mocktracer.Span
	for _, span := range spans {
		// Ignore placeholders without data.
		if span == nil {
			continue
		}
		// Only keep spans that represent tests, not suites or infrastructure.
		if tag := span.Tag(ext.SpanType); tag == constants.SpanTypeTest {
			out = append(out, span)
		}
	}
	return out
}

// spansByResource returns the subset of spans whose resource matches the provided identifier.
func spansByResource(spans []*mocktracer.Span, resource string) []*mocktracer.Span {
	var out []*mocktracer.Span
	for _, span := range spans {
		// Skip nil entries that cannot hold tags.
		if span == nil {
			continue
		}
		// Collect spans that target the requested resource name.
		if value, ok := span.Tag(ext.ResourceName).(string); ok && value == resource {
			out = append(out, span)
		}
	}
	return out
}

// requireSpanCount asserts that a span slice contains exactly the amount expected for the scenario.
func requireSpanCount(spans []*mocktracer.Span, expected int, label string) {
	// Panic with context when the scenario produced an unexpected number of spans.
	if len(spans) != expected {
		panic(fmt.Sprintf("%s: expected %d spans, got %d", label, expected, len(spans)))
	}
}

// assertTagCount enforces that a precise number of spans expose a tag/value pair, mirroring
// telemetry expectations such as retry counters.
func assertTagCount(spans []*mocktracer.Span, key string, value string, expected int, label string) {
	var count int
	for _, span := range spans {
		if span == nil {
			continue
		}
		if tag, ok := span.Tag(key).(string); ok && tag == value {
			count++
		}
	}
	if os.Getenv("SUBTEST_MATRIX_DEBUG") == "1" {
		debugMatrixf("%s: observed %d spans with %s=%q", label, count, key, value)
	}
	if count != expected {
		panic(fmt.Sprintf("%s: expected %d spans with tag %s=%q, got %d", label, expected, key, value, count))
	}
}

// assertTagEquals verifies that a span tag matches the desired value and fails fast otherwise.
func assertTagEquals(span *mocktracer.Span, key string, want string, label string) {
	if span == nil {
		panic(fmt.Sprintf("%s: span is nil", label))
	}
	if value, _ := span.Tag(key).(string); value != want {
		panic(fmt.Sprintf("%s: expected tag %s=%q, got %q", label, key, want, value))
	}
}

// assertTagNotTrue ensures a boolean-like tag is either absent or false, useful when testing inheritance.
func assertTagNotTrue(span *mocktracer.Span, key string, label string) {
	if span == nil {
		return
	}
	if value, ok := span.Tag(key).(string); ok && value == "true" {
		panic(fmt.Sprintf("%s: expected tag %s to be absent/false, got true", label, key))
	}
}

// matrixScenarioNames returns the list of scenario identifiers executed by TestMain.
func matrixScenarioNames() []string {
	names := make([]string, 0, len(availableScenarios))
	for _, sc := range availableScenarios {
		// Skip placeholder slots that may be left empty in future expansions.
		if sc == nil {
			continue
		}
		names = append(names, sc.name)
	}
	return names
}

// runMatrixScenario executes a specific scenario in isolation by configuring the mock backend,
// running the go test harness, and validating the resulting spans.
func runMatrixScenario(m *testing.M, scenario string) int {
	sc, ok := scenarioByName[scenario]
	// Abort quickly when the requested scenario does not exist.
	if !ok {
		fmt.Printf("unknown subtest matrix scenario: %s\n", scenario)
		return 1
	}

	ctx := newScenarioContext()
	sc.configure(ctx)
	debugMatrixf("scenario %s management data: %+v", scenario, ctx.data)

	envSnapshots := []envSnapshot{setEnv("RUN_SUBTEST_CONTROLLER", "1")}
	for key, value := range ctx.env {
		envSnapshots = append(envSnapshots, setEnv(key, value))
	}
	defer func() {
		for i := len(envSnapshots) - 1; i >= 0; i-- {
			envSnapshots[i].restore()
		}
	}()

	_, restore := startSubtestServer(subtestServerConfig{
		managementData:      ctx.data,
		attemptToFixRetries: ctx.attemptToFixRetries,
	})
	defer restore()

	settings := integrations.GetSettings()
	if settings != nil {
		settings.SubtestFeaturesEnabled = true
		if os.Getenv("SUBTEST_MATRIX_DEBUG") == "1" {
			fmt.Printf("subtest matrix: settings.SubtestFeaturesEnabled=%t\n", settings.SubtestFeaturesEnabled)
		}
	} else if os.Getenv("SUBTEST_MATRIX_DEBUG") == "1" {
		fmt.Printf("subtest matrix: settings unavailable\n")
	}

	tracer := integrations.InitializeCIVisibilityMock()

	exitCode := gotesting.RunM(m)
	// When the run fails, dump span resources for easier diagnosis.
	if exitCode != 0 {
		finished := tracer.FinishedSpans()
		debugMatrixf("scenario %s exit code %d with %d spans", scenario, exitCode, len(finished))
		for i, span := range finished {
			// Skip nil entries yet keep the loop for consistent indices.
			if span == nil {
				continue
			}
			// Provide per-span resource names to speed up debugging.
			if resource, ok := span.Tag(ext.ResourceName).(string); ok {
				debugMatrixf("  span[%d] resource=%s status=%v", i, resource, span.Tag(constants.TestStatus))
			}
		}
		return exitCode
	}

	sc.validate(tracer.FinishedSpans())

	return 0
}

// debugMatrixf prints scenario scoped diagnostics when SUBTEST_MATRIX_DEBUG is enabled.
func debugMatrixf(format string, args ...interface{}) {
	if os.Getenv("SUBTEST_MATRIX_DEBUG") == "1" {
		fmt.Printf(format+"\n", args...)
	}
}

type envSnapshot struct {
	key   string
	value string
	had   bool
}

// setEnv overrides an environment variable and returns a snapshot that can restore it.
func setEnv(key, value string) envSnapshot {
	prev, had := os.LookupEnv(key)
	if err := os.Setenv(key, value); err != nil {
		panic(err)
	}
	return envSnapshot{key: key, value: prev, had: had}
}

func (s envSnapshot) restore() {
	var err error
	if s.had {
		err = os.Setenv(s.key, s.value)
	} else {
		err = os.Unsetenv(s.key)
	}
	if err != nil {
		panic(err)
	}
}

type subtestServerConfig struct {
	managementData      *net.TestManagementTestsResponseDataModules
	attemptToFixRetries int
}

// startSubtestServer spins up the mock backend that feeds settings and management payloads to the harness.
func startSubtestServer(cfg subtestServerConfig) (*httptest.Server, func()) {
	if cfg.managementData == nil {
		cfg.managementData = &net.TestManagementTestsResponseDataModules{
			Modules: make(map[string]net.TestManagementTestsResponseDataSuites),
		}
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Route each request to a stub mirroring the Datadog backend endpoints.
		switch r.URL.Path {
		case "/api/v2/libraries/tests/services/setting":
			// Provide the library settings response required to enable management.
			debugMatrixf("subtest server: settings request")
			defer r.Body.Close()
			w.Header().Set("Content-Type", "application/json")
			var settings net.SettingsResponseData
			settings.CodeCoverage = false
			settings.FlakyTestRetriesEnabled = false
			settings.ItrEnabled = false
			settings.TestsSkipping = false
			settings.KnownTestsEnabled = false
			settings.ImpactedTestsEnabled = false
			settings.EarlyFlakeDetection.Enabled = false
			settings.TestManagement.Enabled = true
			settings.TestManagement.AttemptToFixRetries = cfg.attemptToFixRetries
			resp := struct {
				Data struct {
					ID         string                   `json:"id"`
					Type       string                   `json:"type"`
					Attributes net.SettingsResponseData `json:"attributes"`
				} `json:"data"`
			}{}
			resp.Data.ID = "settings"
			resp.Data.Type = "ci_app_libraries_tests_settings"
			resp.Data.Attributes = settings
			if err := json.NewEncoder(w).Encode(&resp); err != nil {
				panic(err)
			}
		case "/api/v2/test/libraries/test-management/tests":
			// Serve the management payload that drives scenario directives.
			debugMatrixf("subtest server: test-management request")
			defer r.Body.Close()
			w.Header().Set("Content-Type", "application/json")
			if os.Getenv("SUBTEST_MATRIX_DEBUG") == "1" {
				if payload, err := json.Marshal(cfg.managementData); err == nil {
					fmt.Printf("subtest server: management payload %s\n", payload)
				}
			}
			resp := struct {
				Data struct {
					ID         string                                     `json:"id"`
					Type       string                                     `json:"type"`
					Attributes net.TestManagementTestsResponseDataModules `json:"attributes"`
				} `json:"data"`
			}{}
			resp.Data.ID = "test-management"
			resp.Data.Type = "ci_app_libraries_tests"
			resp.Data.Attributes = *cfg.managementData
			if err := json.NewEncoder(w).Encode(&resp); err != nil {
				panic(err)
			}
		case "/api/v2/ci/libraries/tests":
			// Return an empty known-tests payload to satisfy the client.
			debugMatrixf("subtest server: known-tests request")
			defer r.Body.Close()
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.Copy(io.Discard, r.Body)
			w.Write([]byte(`{"data":{"attributes":{"tests":{}}}}`))
		case "/api/v2/git/repository/search_commits":
			// Stub git search commits used during CI Visibility bootstrap.
			debugMatrixf("subtest server: search-commits request")
			defer r.Body.Close()
			_, _ = io.Copy(io.Discard, r.Body)
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{}`))
		case "/api/v2/git/repository/packfile":
			// Accept packfile uploads even though the sandbox blocks writes.
			debugMatrixf("subtest server: packfile request")
			defer r.Body.Close()
			_, _ = io.Copy(io.Discard, r.Body)
			w.WriteHeader(http.StatusAccepted)
		case "/api/v2/logs":
			// Consume CI Visibility logs (ignored during tests) to prevent backpressure.
			debugMatrixf("subtest server: logs intake request")
			defer r.Body.Close()
			var reader io.Reader = r.Body
			if r.Header.Get("Content-Encoding") == "gzip" {
				gz, err := gzip.NewReader(r.Body)
				if err == nil {
					defer gz.Close()
					reader = gz
				}
			}
			_, _ = io.Copy(io.Discard, reader)
			w.WriteHeader(http.StatusAccepted)
		default:
			http.NotFound(w, r)
		}
	})

	server := httptest.NewServer(handler)

	snapshots := []envSnapshot{
		setEnv(constants.CIVisibilityEnabledEnvironmentVariable, "1"),
		setEnv(constants.CIVisibilityAgentlessEnabledEnvironmentVariable, "1"),
		setEnv(constants.CIVisibilityAgentlessURLEnvironmentVariable, server.URL),
		setEnv(constants.APIKeyEnvironmentVariable, "test-api-key"),
		setEnv(constants.CIVisibilityTestManagementAttemptToFixRetriesEnvironmentVariable, strconv.Itoa(cfg.attemptToFixRetries)),
	}

	cleanup := func() {
		for i := len(snapshots) - 1; i >= 0; i-- {
			snapshots[i].restore()
		}
		server.Close()
	}

	return server, cleanup
}
