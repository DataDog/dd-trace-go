// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package net

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal/bazel"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

const (
	// FormatLCOV is the LCOV coverage report format accepted by the coverage report intake.
	FormatLCOV = "lcov"

	// coverageReportSubDomain is the subdomain for the coverage report endpoint.
	coverageReportSubDomain string = "ci-intake"
	// coverageReportURLPath is the URL path for the coverage report endpoint.
	coverageReportURLPath string = "api/v2/cicovreprt"
)

// NewClientForCoverageReportUpload creates a new client for sending code coverage reports.
func NewClientForCoverageReportUpload() Client {
	return NewClientWithServiceNameAndSubdomain("", coverageReportSubDomain)
}

// SendCoverageReport sends a code coverage report to the backend.
func (c *client) SendCoverageReport(report io.Reader, format string) error {
	if report == nil {
		return errors.New("coverage report is nil")
	}
	if format != FormatLCOV {
		return fmt.Errorf("unsupported coverage report format: %s", format)
	}

	if bazel.IsPayloadFilesModeEnabled() {
		log.Debug("civisibility.coverage_report: payload transport mode is file; skipping report upload [format:%s]", format)
		return nil
	}

	reportBytes, err := io.ReadAll(report)
	if err != nil {
		return fmt.Errorf("failed to read coverage report: %w", err)
	}
	compressedReport, err := compressData(reportBytes)
	if err != nil {
		return fmt.Errorf("failed to gzip coverage report: %w", err)
	}

	log.Debug(
		"civisibility.coverage_report: payload transport mode is http [format:%s url:%s report_bytes:%d compressed_report_bytes:%d]",
		format,
		c.getURLPath(coverageReportURLPath),
		len(reportBytes),
		len(compressedReport),
	)

	files := []FormFile{
		{
			FieldName:   "event",
			ContentType: ContentTypeJSON,
			FileName:    "event.json",
			Content:     coverageReportEvent(format),
		},
		{
			FieldName:   "coverage",
			ContentType: ContentTypeOctetStream,
			FileName:    "coverage.gz",
			Content:     compressedReport,
		},
	}
	multipartBody, contentType, err := createMultipartFormData(files, false)
	if err != nil {
		return fmt.Errorf("failed to create coverage report request body: %w", err)
	}

	request := RequestConfig{
		Method:         "POST",
		URL:            c.getURLPath(coverageReportURLPath),
		Headers:        c.headers,
		RawBody:        multipartBody,
		RawContentType: contentType,
	}

	telemetry.CoverageUploadRequest(telemetry.UncompressedRequestCompressedType)
	telemetry.CoverageUploadRequestBytes(telemetry.UncompressedRequestCompressedType, float64(len(multipartBody)))

	startTime := time.Now()
	response, responseErr := c.handler.SendRequest(request)
	durationMs := time.Since(startTime).Milliseconds()
	telemetry.CoverageUploadRequestMs(float64(durationMs))

	if responseErr != nil {
		telemetry.CoverageUploadRequestErrors(telemetry.NetworkErrorType)
		return fmt.Errorf("failed to send coverage report request: %s", responseErr)
	}

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		telemetry.CoverageUploadRequestErrors(telemetry.GetErrorTypeFromStatusCode(response.StatusCode))
		return fmt.Errorf("unexpected response code %d: %s", response.StatusCode, string(response.Body))
	}

	log.Debug(
		"civisibility.coverage_report: upload request completed [status_code:%d duration_ms:%d compressed_report_bytes:%d request_body_bytes:%d]",
		response.StatusCode,
		durationMs,
		len(compressedReport),
		len(multipartBody),
	)
	return nil
}

func coverageReportEvent(format string) map[string]string {
	event := map[string]string{
		"type":   "coverage_report",
		"format": format,
	}

	for key, value := range utils.GetCITags() {
		if value == "" {
			continue
		}
		if strings.HasPrefix(key, "git.") || strings.HasPrefix(key, "ci.") || key == constants.PrNumber {
			event[key] = value
		}
	}

	return event
}
