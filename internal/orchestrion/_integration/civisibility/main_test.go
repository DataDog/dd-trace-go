// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

package civisibility

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/DataDog/orchestrion/runtime/built"
	"github.com/tinylib/msgp/msgp"

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
)

var ciVisibilityPayloads mockPayloads

func TestMain(m *testing.M) {
	// check if orchestrion is enabled
	if !built.WithOrchestrion {
		panic("Orchestrion is not enabled, please run this test with orchestrion")
	}

	// let's enable CI Visibility mode
	server := enableCiVisibilityEndpointMock()
	defer server.Close()

	// because CI Visibility mode is enabled all tests are going to be instrumented
	// we have a few tests to check the different test statuses (including failures)
	// that's why we don't use the exit code from the tests, but we check the events.
	m.Run()

	// let's check the events inside the CiVisibility payloads
	events := ciVisibilityPayloads.GetEvents()

	// session event
	events.
		CheckEventsByType("test_session_end", 1)

	// module event
	events.
		CheckEventsByType("test_module_end", 1).
		CheckEventsByResourceName("github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/civisibility", 1)

	// test suite event
	events.CheckEventsByType("test_suite_end", 1).
		CheckEventsByResourceName("testing_test.go", 1)

	// test events
	testEvents := events.CheckEventsByType("test", 15)
	normalTests := testEvents.
		CheckEventsByResourceName("testing_test.go.TestNormal", 1).
		CheckEventsByTagAndValue("test.status", "pass", 1)
	failTests := testEvents.
		CheckEventsByResourceName("testing_test.go.TestFail", 1).
		CheckEventsByTagAndValue("test.status", "fail", 1).
		CheckEventsByTagAndValue("error.type", "Fail", 1).
		CheckEventsByTagAndValue("error.message", "failed test", 1)
	errorTests := testEvents.
		CheckEventsByResourceName("testing_test.go.TestError", 1).
		CheckEventsByTagAndValue("test.status", "fail", 1).
		CheckEventsByTagAndValue("error.type", "Error", 1).
		CheckEventsByTagAndValue("error.message", "My error test", 1)
	errorFTests := testEvents.
		CheckEventsByResourceName("testing_test.go.TestErrorf", 1).
		CheckEventsByTagAndValue("test.status", "fail", 1).
		CheckEventsByTagAndValue("error.type", "Errorf", 1).
		CheckEventsByTagAndValue("error.message", "My error test: TestErrorf", 1)
	skipTests := testEvents.
		CheckEventsByResourceName("testing_test.go.TestSkip", 1).
		CheckEventsByTagAndValue("test.status", "skip", 1).
		CheckEventsByTagAndValue("test.skip_reason", "My skipped test", 1)
	skipfTests := testEvents.
		CheckEventsByResourceName("testing_test.go.TestSkipf", 1).
		CheckEventsByTagAndValue("test.status", "skip", 1).
		CheckEventsByTagAndValue("test.skip_reason", "My skipped test: TestSkipf", 1)
	skipNowTests := testEvents.
		CheckEventsByResourceName("testing_test.go.TestSkipNow", 1).
		CheckEventsByTagAndValue("test.status", "skip", 1)
	testWithSubtests := testEvents.
		CheckEventsByResourceName("testing_test.go.TestWithSubTests", 1).
		CheckEventsByTagAndValue("test.status", "pass", 1)
	testWithSubtestsChild1 := testEvents.
		CheckEventsByResourceName("testing_test.go.TestWithSubTests/Sub1", 1).
		CheckEventsByTagAndValue("test.status", "pass", 1)
	testWithSubtestsChild2 := testEvents.
		CheckEventsByResourceName("testing_test.go.TestWithSubTests/Sub2", 1).
		CheckEventsByTagAndValue("test.status", "pass", 1)
	testWithParallelSubtests := testEvents.
		CheckEventsByResourceName("testing_test.go.TestWithParallelSubTests", 1).
		CheckEventsByTagAndValue("test.status", "pass", 1)
	testWithParallelSubtestsChild1 := testEvents.
		CheckEventsByResourceName("testing_test.go.TestWithParallelSubTests/Sub1", 1).
		CheckEventsByTagAndValue("test.status", "pass", 1)
	testWithParallelSubtestsChild2 := testEvents.
		CheckEventsByResourceName("testing_test.go.TestWithParallelSubTests/Sub2", 1).
		CheckEventsByTagAndValue("test.status", "pass", 1)
	testWithParallelSubtestsChild3 := testEvents.
		CheckEventsByResourceName("testing_test.go.TestWithParallelSubTests/Sub3", 1).
		CheckEventsByTagAndValue("test.status", "pass", 1)
	parallelSkip := testEvents.
		CheckEventsByResourceName("testing_test.go.TestParallelSkip", 1).
		CheckEventsByTagAndValue("test.status", "skip", 1).
		CheckEventsByTagAndValue("test.skip_reason", "parallel skip", 1)

	validateParallelTimingPayload(events, testEvents)

	// remaining must be 0
	testEvents.
		Except(
			normalTests,
			failTests,
			errorTests,
			errorFTests,
			skipTests,
			skipfTests,
			skipNowTests,
			testWithSubtests,
			testWithSubtestsChild1,
			testWithSubtestsChild2,
			testWithParallelSubtests,
			testWithParallelSubtestsChild1,
			testWithParallelSubtestsChild2,
			testWithParallelSubtestsChild3,
			parallelSkip).
		HasCount(0)

	// All previous checks will cause panic if they fail so we can safely exit with 0 here
	os.Exit(0)
}

func validateParallelTimingPayload(events, testEvents mockEvents) {
	parallelTests := make(map[uint64]mockEvent, 5)
	for _, event := range testEvents {
		active, ok := event.Content.Metrics[constants.TestActiveDuration]
		if !ok || active < 0 {
			panic(fmt.Sprintf("test event is missing a valid %s metric: %+v", constants.TestActiveDuration, event))
		}
		isParallel, ok := event.Content.Meta[constants.TestIsParallel]
		if !ok || (isParallel != "true" && isParallel != "false") {
			panic(fmt.Sprintf("test event is missing a valid %s tag: %+v", constants.TestIsParallel, event))
		}

		_, hasStart := event.Content.Metrics[constants.TestParallelPauseStartOffset]
		_, hasEnd := event.Content.Metrics[constants.TestParallelPauseEndOffset]
		_, hasDuration := event.Content.Metrics[constants.TestParallelPauseDuration]
		if isParallel == "false" {
			if hasStart || hasEnd || hasDuration {
				panic(fmt.Sprintf("non-parallel test event has parallel pause metrics: %+v", event))
			}
			continue
		}
		if !hasStart || !hasEnd || !hasDuration {
			panic(fmt.Sprintf("parallel test event is missing pause metrics: %+v", event))
		}
		parallelTests[event.Content.SpanID] = event
	}
	if len(parallelTests) != 5 {
		panic(fmt.Sprintf("expected exactly 5 parallel test events, got %d", len(parallelTests)))
	}

	waits := events.GetEventsByResourceName("testing.T.Parallel").CheckEventsByType("span", len(parallelTests))
	for _, wait := range waits {
		if wait.Content.Name != "testing.parallel.wait" || wait.Content.Type != "" ||
			wait.Content.Meta["component"] != "go-testing" || wait.Content.Meta["test.parallel.wait"] != "true" {
			panic(fmt.Sprintf("unexpected parallel wait event: %+v", wait))
		}
		parent, ok := parallelTests[wait.Content.ParentID]
		if !ok || wait.Content.TraceID != parent.Content.TraceID {
			panic(fmt.Sprintf("parallel wait event has no test parent in its trace: %+v", wait))
		}
		delete(parallelTests, wait.Content.ParentID)

		startOffset := parent.Content.Metrics[constants.TestParallelPauseStartOffset]
		endOffset := parent.Content.Metrics[constants.TestParallelPauseEndOffset]
		pauseDuration := parent.Content.Metrics[constants.TestParallelPauseDuration]
		activeDuration := parent.Content.Metrics[constants.TestActiveDuration]
		if startOffset < 0 || endOffset < startOffset || pauseDuration != endOffset-startOffset {
			panic(fmt.Sprintf("invalid parallel timing metrics: %+v", parent))
		}
		if wait.Content.Start != parent.Content.Start+uint64(startOffset) || float64(wait.Content.Duration) != pauseDuration {
			panic(fmt.Sprintf("parallel wait event does not match its parent metrics: wait=%+v parent=%+v", wait, parent))
		}
		if activeDuration+pauseDuration > float64(parent.Content.Duration)+float64(time.Millisecond) {
			panic(fmt.Sprintf("parallel active and pause durations exceed wall time: %+v", parent))
		}

		switch parent.Content.Resource {
		case "testing_test.go.TestWithParallelSubTests/Sub1",
			"testing_test.go.TestWithParallelSubTests/Sub2",
			"testing_test.go.TestWithParallelSubTests/Sub3":
			minimumPause := float64(parallelPayloadBlockerDuration - 25*time.Millisecond)
			if pauseDuration < minimumPause || float64(parent.Content.Duration)-activeDuration < minimumPause {
				panic(fmt.Sprintf("parallel child timing includes the serial blocker: %+v", parent))
			}
		}
	}
	if len(parallelTests) != 0 {
		panic(fmt.Sprintf("parallel test events are missing wait children: %+v", parallelTests))
	}
}

func enableCiVisibilityEndpointMock() *httptest.Server {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/citestcycle" {
			w.WriteHeader(http.StatusNotFound)
			return
		}

		fmt.Printf("mockapi: test cycle payload received.\n")

		// first we need to read the body
		// then we need to gunzip the body
		// then we need to convert the body from msgpack to json
		// then we need to parse the json

		gzipReader, err := gzip.NewReader(r.Body)
		if err != nil {
			log.Println(err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		defer gzipReader.Close()

		// Convert the message pack to json
		var jsonBuf bytes.Buffer
		_, err = msgp.CopyToJSON(&jsonBuf, gzipReader)
		if err != nil {
			log.Println(err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		var payload mockPayload
		err = json.Unmarshal(jsonBuf.Bytes(), &payload)
		if err != nil {
			log.Println(err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		ciVisibilityPayloads = append(ciVisibilityPayloads, &payload)
		w.WriteHeader(http.StatusAccepted)
	}))

	fmt.Printf("mockapi: Url: %s\n", server.URL)

	os.Setenv("DD_CIVISIBILITY_ENABLED", "true")
	os.Setenv("DD_CIVISIBILITY_AGENTLESS_ENABLED", "true")
	os.Setenv("DD_CIVISIBILITY_AGENTLESS_URL", server.URL)
	os.Setenv("DD_API_KEY", "***")

	return server
}
