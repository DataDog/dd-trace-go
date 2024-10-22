// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

//go:generate msgp -unexported -marshal=false -o=test_coverage_msgp.go -tests=false

package coverage

import "github.com/tinylib/msgp/msgp"

type (
	// ciTestCoveragePayloads represents a list of test code coverage payloads.
	ciTestCoveragePayloads []*ciTestCovPayload

	// ciTestCoverages represents a list of test code coverage data.
	ciTestCoverages []*ciTestCoverageData

	// ciTestCovPayload represents a test code coverage payload specifically designed for CI Visibility events.
	ciTestCovPayload struct {
		Version   int32    `msg:"version"`   // Version of the payload
		Coverages msgp.Raw `msg:"coverages"` // list of coverages
	}

	// ciTestCoverageData represents the coverage data for a single test.
	ciTestCoverageData struct {
		SessionID uint64                `msg:"test_session_id"` // identifier of this session
		SuiteID   uint64                `msg:"test_suite_id"`   // identifier of the suite
		SpanID    uint64                `msg:"span_id"`         // identifier of this test
		Files     []*ciTestCoverageFile `msg:"files"`           // list of files covered
	}

	// ciTestCoverageFile represents the coverage data for a single file.
	ciTestCoverageFile struct {
		FileName string `msg:"filename"` // name of the file
	}
)

var (
	_ msgp.Encodable = (*ciTestCoverageData)(nil)
	_ msgp.Decodable = (*ciTestCoverageData)(nil)

	_ msgp.Encodable = (*ciTestCoverages)(nil)
	_ msgp.Decodable = (*ciTestCoverages)(nil)

	_ msgp.Encodable = (*ciTestCovPayload)(nil)
	_ msgp.Decodable = (*ciTestCoveragePayloads)(nil)
)

// newCiTestCovPayload creates a new instance of ciTestCovPayload.
func newCiTestCoverageData(tCove *testCoverage) *ciTestCoverageData {
	return &ciTestCoverageData{
		SessionID: tCove.sessionID,
		SuiteID:   tCove.suiteID,
		SpanID:    tCove.testID,
		Files:     newCiTestCoverageFiles(tCove.filesCovered),
	}
}

// newCiTestCoverageFiles creates a new instance of ciTestCoverageFile array.
func newCiTestCoverageFiles(files []string) []*ciTestCoverageFile {
	ciFiles := make([]*ciTestCoverageFile, 0, len(files))
	for _, file := range files {
		ciFiles = append(ciFiles, &ciTestCoverageFile{FileName: file})
	}
	return ciFiles
}
