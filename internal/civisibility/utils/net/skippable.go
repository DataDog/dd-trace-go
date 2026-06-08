// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package net

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal/bazel"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils/filebitmap"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils/telemetry"
)

const (
	skippableRequestType string = "test_params"
	skippableURLPath     string = "api/v2/ci/tests/skippable"
)

type (
	skippableRequest struct {
		Data skippableRequestHeader `json:"data"`
	}

	skippableRequestHeader struct {
		Type       string               `json:"type"`
		Attributes skippableRequestData `json:"attributes"`
	}

	skippableRequestData struct {
		TestLevel      string             `json:"test_level"`
		Configurations testConfigurations `json:"configurations"`
		Service        string             `json:"service"`
		Env            string             `json:"env"`
		RepositoryURL  string             `json:"repository_url"`
		Sha            string             `json:"sha"`
	}

	skippableResponse struct {
		Meta skippableResponseMeta   `json:"meta"`
		Data []skippableResponseData `json:"data"`
	}

	skippableResponseMeta struct {
		CorrelationID string          `json:"correlation_id"`
		Coverage      json.RawMessage `json:"coverage"`
	}

	skippableResponseData struct {
		ID         string                          `json:"id"`
		Type       string                          `json:"type"`
		Attributes SkippableResponseDataAttributes `json:"attributes"`
	}

	SkippableResponseDataAttributes struct {
		Suite                   string             `json:"suite"`
		Name                    string             `json:"name"`
		Parameters              string             `json:"parameters"`
		Configurations          testConfigurations `json:"configurations"`
		MissingLineCodeCoverage bool               `json:"_missing_line_code_coverage"`
	}

	// SkippableTestsResponse stores the skippable-tests response plus backend
	// line coverage metadata used to decide whether coverage-active ITR skips
	// can be applied safely.
	SkippableTestsResponse struct {
		CorrelationID          string
		Skippables             map[string]map[string][]SkippableResponseDataAttributes
		Coverage               map[string]*filebitmap.FileBitmap
		CoveragePresent        bool
		CoverageBackfillSafe   bool
		CoverageBackfillReason string
		ResponseTestsCount     int
	}

	// cachedSkippableTests stores the skippable payload plus original response count.
	cachedSkippableTests struct {
		CorrelationID      string                                                  `json:"correlation_id"`
		Skippables         map[string]map[string][]SkippableResponseDataAttributes `json:"skippables"`
		Coverage           map[string]string                                       `json:"coverage,omitempty"`
		CoveragePresent    bool                                                    `json:"coverage_present"`
		CoverageSafe       bool                                                    `json:"coverage_backfill_safe"`
		CoverageReason     string                                                  `json:"coverage_backfill_reason"`
		ResponseTestsCount int                                                     `json:"response_tests_count"`
	}
)

const (
	coverageBackfillReasonMissing = "backend coverage metadata missing"
	coverageBackfillReasonInvalid = "backend coverage metadata invalid"
	coverageBackfillReasonEmpty   = "backend coverage metadata empty"
)

func (c *client) GetSkippableTests() (*SkippableTestsResponse, error) {
	if bazel.IsManifestModeEnabled() {
		return &SkippableTestsResponse{
			Skippables:             map[string]map[string][]SkippableResponseDataAttributes{},
			CoverageBackfillReason: "bazel coverage mode unsupported",
		}, nil
	}

	if c.repositoryURL == "" || c.commitSha == "" {
		return nil, fmt.Errorf("civisibility.GetSkippableTests: repository URL and commit SHA are required")
	}

	body := skippableRequest{
		Data: skippableRequestHeader{
			Type: skippableRequestType,
			Attributes: skippableRequestData{
				TestLevel:      "test",
				Configurations: c.testConfigurations,
				Service:        c.serviceName,
				Env:            c.environment,
				RepositoryURL:  c.repositoryURL,
				Sha:            c.commitSha,
			},
		},
	}

	result, err := readThroughShortLivedCache(
		c,
		readCacheEndpointSkippableTests,
		body,
		func() (readCacheLiveResult[cachedSkippableTests], error) {
			request := c.getPostRequestConfig(skippableURLPath, body)
			if request.Compressed {
				telemetry.ITRSkippableTestsRequest(telemetry.CompressedRequestCompressedType)
			} else {
				telemetry.ITRSkippableTestsRequest(telemetry.UncompressedRequestCompressedType)
			}

			startTime := time.Now()
			response, err := c.handler.SendRequest(*request)
			telemetry.ITRSkippableTestsRequestMs(float64(time.Since(startTime).Milliseconds()))

			if err != nil {
				telemetry.ITRSkippableTestsRequestErrors(telemetry.NetworkErrorType)
				return readCacheLiveResult[cachedSkippableTests]{}, fmt.Errorf("sending skippable tests request: %s", err)
			}

			if response.StatusCode < 200 || response.StatusCode >= 300 {
				telemetry.ITRSkippableTestsRequestErrors(telemetry.GetErrorTypeFromStatusCode(response.StatusCode))
			}

			if response.Compressed {
				telemetry.ITRSkippableTestsResponseBytes(telemetry.CompressedResponseCompressedType, float64(len(response.Body)))
			} else {
				telemetry.ITRSkippableTestsResponseBytes(telemetry.UncompressedResponseCompressedType, float64(len(response.Body)))
			}

			var responseObject skippableResponse
			err = response.Unmarshal(&responseObject)
			if err != nil {
				return readCacheLiveResult[cachedSkippableTests]{}, fmt.Errorf("unmarshalling skippable tests response: %s", err)
			}

			coverage, coveragePresent, coverageSafe, coverageReason, coverageParseError := parseSkippableCoverage(responseObject.Meta.Coverage)
			if coverageParseError {
				telemetry.CodeCoverageErrors()
			}

			responseTestsCount := len(responseObject.Data)
			telemetry.ITRSkippableTestsResponseTests(float64(responseTestsCount))
			skippableTestsMap := map[string]map[string][]SkippableResponseDataAttributes{}
			for _, data := range responseObject.Data {
				var ok bool
				var testsMap map[string][]SkippableResponseDataAttributes
				if testsMap, ok = skippableTestsMap[data.Attributes.Suite]; !ok {
					testsMap = map[string][]SkippableResponseDataAttributes{}
					skippableTestsMap[data.Attributes.Suite] = testsMap
				}

				if test, ok := testsMap[data.Attributes.Name]; ok {
					testsMap[data.Attributes.Name] = append(test, data.Attributes)
				} else {
					testsMap[data.Attributes.Name] = []SkippableResponseDataAttributes{data.Attributes}
				}
			}

			value := cachedSkippableTests{
				CorrelationID:      responseObject.Meta.CorrelationID,
				Skippables:         skippableTestsMap,
				Coverage:           encodeSkippableCoverage(coverage),
				CoveragePresent:    coveragePresent,
				CoverageSafe:       coverageSafe,
				CoverageReason:     coverageReason,
				ResponseTestsCount: responseTestsCount,
			}
			return readCacheLiveResult[cachedSkippableTests]{
				Value:     value,
				Cacheable: response.StatusCode >= 200 && response.StatusCode < 300,
			}, nil
		},
		func(cached cachedSkippableTests) {
			telemetry.ITRSkippableTestsResponseTests(float64(cached.ResponseTestsCount))
		},
	)
	if err != nil {
		return nil, err
	}
	return result.toResponse(), nil
}

func parseSkippableCoverage(raw json.RawMessage) (map[string]*filebitmap.FileBitmap, bool, bool, string, bool) {
	if len(raw) == 0 || strings.EqualFold(strings.TrimSpace(string(raw)), "null") {
		return nil, false, false, coverageBackfillReasonMissing, false
	}

	var encoded map[string]string
	if err := json.Unmarshal(raw, &encoded); err != nil {
		return nil, true, false, coverageBackfillReasonInvalid, true
	}
	if len(encoded) == 0 {
		return nil, true, false, coverageBackfillReasonEmpty, false
	}

	coverage := make(map[string]*filebitmap.FileBitmap, len(encoded))
	for rawPath, rawBitmap := range encoded {
		normalized, err := normalizeSkippableCoveragePath(rawPath)
		if err != nil {
			return nil, true, false, coverageBackfillReasonInvalid, true
		}
		decoded, err := base64.StdEncoding.DecodeString(rawBitmap)
		if err != nil {
			return nil, true, false, coverageBackfillReasonInvalid, true
		}
		bitmap := newFileBitmapFromJavaBitSet(decoded)
		if existing, ok := coverage[normalized]; ok {
			coverage[normalized] = filebitmap.Or(existing, bitmap, false)
		} else {
			coverage[normalized] = bitmap
		}
	}

	return coverage, true, true, "", false
}

func normalizeSkippableCoveragePath(rawPath string) (string, error) {
	normalized := strings.TrimSpace(strings.ReplaceAll(rawPath, "\\", "/"))
	if strings.HasPrefix(normalized, "/") {
		normalized = strings.TrimPrefix(normalized, "/")
	}
	if normalized == "" {
		return "", fmt.Errorf("coverage path cannot be empty")
	}
	if path.IsAbs(normalized) || isWindowsDrivePath(normalized) {
		return "", fmt.Errorf("coverage path must be repository relative")
	}
	normalized = path.Clean(normalized)
	if normalized == "." || normalized == "/" || normalized == ".." || strings.HasPrefix(normalized, "../") || isWindowsDrivePath(normalized) {
		return "", fmt.Errorf("coverage path cannot point outside the repository")
	}
	return normalized, nil
}

func isWindowsDrivePath(value string) bool {
	if len(value) < 2 || value[1] != ':' {
		return false
	}
	drive := value[0]
	return (drive >= 'a' && drive <= 'z') || (drive >= 'A' && drive <= 'Z')
}

func newFileBitmapFromJavaBitSet(data []byte) *filebitmap.FileBitmap {
	// The skippable-tests API uses Java BitSet.toByteArray/valueOf semantics:
	// bit index N represents source line N and bytes are little-endian by bit.
	// filebitmap uses one-indexed lines with the first line stored in the most
	// significant bit, so translate only at the API/cache boundary.
	bitmap := filebitmap.FromLineCount(len(data) * 8)
	for line := 1; line < len(data)*8; line++ {
		byteIndex := line / 8
		bitMask := byte(1 << (line % 8))
		if data[byteIndex]&bitMask != 0 {
			bitmap.Set(line)
		}
	}
	return bitmap
}

func javaBitSetFromFileBitmap(bitmap *filebitmap.FileBitmap) []byte {
	// See newFileBitmapFromJavaBitSet for the wire-format contract.
	if bitmap == nil {
		return nil
	}
	data := make([]byte, bitmap.BitCount()/8+1)
	for line := 1; line <= bitmap.BitCount(); line++ {
		if !bitmap.Get(line) {
			continue
		}
		byteIndex := line / 8
		data[byteIndex] |= byte(1 << (line % 8))
	}
	for len(data) > 0 && data[len(data)-1] == 0 {
		data = data[:len(data)-1]
	}
	return data
}

func encodeSkippableCoverage(coverage map[string]*filebitmap.FileBitmap) map[string]string {
	if len(coverage) == 0 {
		return nil
	}
	encoded := make(map[string]string, len(coverage))
	for file, bitmap := range coverage {
		encoded[file] = base64.StdEncoding.EncodeToString(javaBitSetFromFileBitmap(bitmap))
	}
	return encoded
}

func decodeCachedSkippableCoverage(encoded map[string]string) map[string]*filebitmap.FileBitmap {
	if len(encoded) == 0 {
		return nil
	}
	coverage := make(map[string]*filebitmap.FileBitmap, len(encoded))
	for file, bitmap := range encoded {
		decoded, err := base64.StdEncoding.DecodeString(bitmap)
		if err != nil {
			return nil
		}
		coverage[file] = newFileBitmapFromJavaBitSet(decoded)
	}
	return coverage
}

func (c cachedSkippableTests) toResponse() *SkippableTestsResponse {
	return &SkippableTestsResponse{
		CorrelationID:          c.CorrelationID,
		Skippables:             c.Skippables,
		Coverage:               decodeCachedSkippableCoverage(c.Coverage),
		CoveragePresent:        c.CoveragePresent,
		CoverageBackfillSafe:   c.CoverageSafe,
		CoverageBackfillReason: c.CoverageReason,
		ResponseTestsCount:     c.ResponseTestsCount,
	}
}
