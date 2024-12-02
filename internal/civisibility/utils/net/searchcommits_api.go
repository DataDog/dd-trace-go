// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package net

import (
	"fmt"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/utils/telemetry"
)

const (
	searchCommitsType    string = "commit"
	searchCommitsURLPath string = "api/v2/git/repository/search_commits"
)

type (
	searchCommits struct {
		Data []searchCommitsData `json:"data"`
		Meta searchCommitsMeta   `json:"meta"`
	}
	searchCommitsData struct {
		ID   string `json:"id"`
		Type string `json:"type"`
	}
	searchCommitsMeta struct {
		RepositoryURL string `json:"repository_url"`
	}
)

func (c *client) GetCommits(localCommits []string) ([]string, error) {
	body := searchCommits{
		Data: []searchCommitsData{},
		Meta: searchCommitsMeta{
			RepositoryURL: c.repositoryURL,
		},
	}

	for _, localCommit := range localCommits {
		body.Data = append(body.Data, searchCommitsData{
			ID:   localCommit,
			Type: searchCommitsType,
		})
	}

	request := c.getPostRequestConfig(searchCommitsURLPath, body)
	if request.Compressed {
		telemetry.GitRequestsSearchCommits(telemetry.CompressedRequestCompressedType)
	} else {
		telemetry.GitRequestsSearchCommits(telemetry.UncompressedRequestCompressedType)
	}

	startTime := time.Now()
	response, err := c.handler.SendRequest(*request)
	if err != nil {
		telemetry.GitRequestsSearchCommitsErrors(telemetry.NetworkErrorType)
		return nil, fmt.Errorf("sending search commits request: %s", err.Error())
	}

	if response.Compressed {
		telemetry.GitRequestsSearchCommitsMs(telemetry.CompressedResponseCompressedType, float64(time.Since(startTime).Milliseconds()))
	} else {
		telemetry.GitRequestsSearchCommitsMs(telemetry.UncompressedResponseCompressedType, float64(time.Since(startTime).Milliseconds()))
	}

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		telemetry.GitRequestsSearchCommitsErrors(telemetry.GetErrorTypeFromStatusCode(response.StatusCode))
	}

	var responseObject searchCommits
	err = response.Unmarshal(&responseObject)
	if err != nil {
		return nil, fmt.Errorf("unmarshalling search commits response: %s", err.Error())
	}

	var commits []string
	for _, commit := range responseObject.Data {
		commits = append(commits, commit.ID)
	}
	return commits, nil
}
