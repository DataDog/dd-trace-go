// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package net

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/utils/telemetry"
)

const (
	sendPackFilesURLPath string = "api/v2/git/repository/packfile"
)

type (
	pushedShaBody struct {
		Data pushedShaData `json:"data"`
		Meta pushedShaMeta `json:"meta"`
	}
	pushedShaData struct {
		ID   string `json:"id"`
		Type string `json:"type"`
	}
	pushedShaMeta struct {
		RepositoryURL string `json:"repository_url"`
	}
)

func (c *client) SendPackFiles(commitSha string, packFiles []string) (bytes int64, err error) {
	if len(packFiles) == 0 {
		return 0, nil
	}

	if commitSha == "" {
		commitSha = c.commitSha
	}

	pushedShaFormFile := FormFile{
		FieldName: "pushedSha",
		Content: pushedShaBody{
			Data: pushedShaData{
				ID:   commitSha,
				Type: searchCommitsType,
			},
			Meta: pushedShaMeta{
				RepositoryURL: c.repositoryURL,
			},
		},
		ContentType: ContentTypeJSON,
	}

	for _, file := range packFiles {
		fileContent, fileErr := os.ReadFile(file)
		if fileErr != nil {
			err = fmt.Errorf("failed to read pack file: %s", fileErr.Error())
			return
		}

		request := RequestConfig{
			Method:  "POST",
			URL:     c.getURLPath(sendPackFilesURLPath),
			Headers: c.headers,
			Files: []FormFile{
				pushedShaFormFile,
				{
					FieldName:   "packfile",
					Content:     fileContent,
					ContentType: ContentTypeOctetStream,
				},
			},
			MaxRetries: DefaultMaxRetries,
			Backoff:    DefaultBackoff,
		}

		if request.Compressed {
			telemetry.GitRequestsObjectsPack(telemetry.CompressedRequestCompressedType)
		} else {
			telemetry.GitRequestsObjectsPack(telemetry.UncompressedRequestCompressedType)
		}

		startTime := time.Now()
		response, responseErr := c.handler.SendRequest(request)
		telemetry.GitRequestsObjectsPackMs(float64(time.Since(startTime).Milliseconds()))

		if responseErr != nil {
			telemetry.GitRequestsObjectsPackErrors(telemetry.NetworkErrorType)
			err = fmt.Errorf("failed to send packfile request: %s", responseErr.Error())
			return
		}

		if response.StatusCode < 200 || response.StatusCode >= 300 {
			telemetry.GitRequestsObjectsPackErrors(telemetry.GetErrorTypeFromStatusCode(response.StatusCode))
			err = fmt.Errorf("unexpected response code %d: %s", response.StatusCode, string(response.Body))
		}

		bytes += int64(len(fileContent))
	}

	telemetry.GitRequestsObjectsPackFiles(float64(len(packFiles)))
	telemetry.GitRequestsObjectsPackBytes(float64(bytes))
	return
}
