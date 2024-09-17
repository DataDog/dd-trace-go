// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package net

import (
	"fmt"
)

const (
	searchCommitsType    string = "commit"
	searchCommitsURLPath string = "api/v2/git/repository/search_commits"
)

type (
	searchCommits struct {
		Data []searchCommitsData `json:"data,omitempty"`
		Meta searchCommitsMeta   `json:"meta,omitempty"`
	}
	searchCommitsData struct {
		ID   string `json:"id,omitempty"`
		Type string `json:"type,omitempty"`
	}
	searchCommitsMeta struct {
		RepositoryURL string `json:"repository_url,omitempty"`
	}
)

func (c *client) GetCommits(localCommits []string) ([]string, error) {
	body := searchCommits{
		Data: []searchCommitsData{},
		Meta: searchCommitsMeta{
			RepositoryURL: c.repositoryUrl,
		},
	}

	for _, localCommit := range localCommits {
		body.Data = append(body.Data, searchCommitsData{
			ID:   localCommit,
			Type: searchCommitsType,
		})
	}

	response, err := c.handler.SendRequest(*c.getPostRequestConfig(searchCommitsURLPath, body))
	if err != nil {
		return nil, fmt.Errorf("sending search commits request: %s", err.Error())
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
