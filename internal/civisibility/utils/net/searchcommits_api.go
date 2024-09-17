// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package net

import "github.com/pkg/errors"

const (
	searchCommitsType    string = "commit"
	searchCommitsUrlPath string = "api/v2/git/repository/search_commits"
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

	response, err := c.handler.SendRequest(*c.getPostRequestConfig(searchCommitsUrlPath, body))
	if err != nil {
		return nil, errors.Wrap(err, "sending search commits request")
	}

	var responseObject searchCommits
	err = response.Unmarshal(&responseObject)
	if err != nil {
		return nil, errors.Wrap(err, "unmarshalling search commits response")
	}

	var commits []string
	for _, commit := range responseObject.Data {
		commits = append(commits, commit.ID)
	}
	return commits, nil
}
