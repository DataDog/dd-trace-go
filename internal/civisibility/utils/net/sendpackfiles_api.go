// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package net

import (
	"fmt"
	"net/http"
	"os"
)

const (
	sendPackFilesURLPath string = "api/v2/git/repository/packfile"
)

type (
	pushedShaBody struct {
		Data pushedShaData `json:"data,omitempty"`
		Meta pushedShaMeta `json:"meta,omitempty"`
	}
	pushedShaData struct {
		ID   string `json:"id,omitempty"`
		Type string `json:"type,omitempty"`
	}
	pushedShaMeta struct {
		RepositoryURL string `json:"repository_url,omitempty"`
	}
)

func (c *client) SendPackFiles(packFiles []string) (bytes int64, err error) {
	if len(packFiles) == 0 {
		return 0, nil
	}

	pushedShaFormFile := FormFile{
		FieldName: "pushedSha",
		Content: pushedShaBody{
			Data: pushedShaData{
				ID:   c.commitSha,
				Type: searchCommitsType,
			},
			Meta: pushedShaMeta{
				RepositoryURL: c.repositoryUrl,
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
			URL:     c.getUrlPath(sendPackFilesURLPath),
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

		response, responseErr := c.handler.SendRequest(request)
		if responseErr != nil {
			err = fmt.Errorf("failed to send packfile request: %s", responseErr.Error())
			return
		}

		if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusNoContent {
			err = fmt.Errorf("unexpected response code %d: %s", response.StatusCode, string(response.Body))
		}

		bytes += int64(len(fileContent))
	}

	return
}
