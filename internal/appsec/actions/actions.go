// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package actions

import (
	"encoding/json"
	"strconv"
)

type (
	// ActionEntry represents an entry in the actions field of a rules file
	ActionEntry[T any] struct {
		ID         string `json:"id"`
		Type       string `json:"type"`
		Parameters T      `json:"parameters"`
	}

	// BlockActionParams are the dynamic parameters to be provided to a "block_request"
	// action type upon invocation
	BlockActionParams struct {
		GRPCStatusCode *int   `json:"grpc_status_code,omitempty"`
		StatusCode     int    `json:"status_code"`
		Type           string `json:"type,omitempty"`
	}
	// RedirectActionParams are the dynamic parameters to be provided to a "redirect_request"
	// action type upon invocation
	RedirectActionParams struct {
		Location   string `json:"location,omitempty"`
		StatusCode int    `json:"status_code"`
	}
)

// BlockParamsFromMap fills a BlockActionParams struct from the the map returned by the WAF
// for a "block_request" action type. This map currently maps all param values to string which
// is why we first peform a decoding to string, before converting.
// Future WAF version may get rid of this string-only mapping, which would in turn make this process
// a lot simpler
func BlockParamsFromMap(params map[string]any) (BlockActionParams, error) {
	type blockActionParams struct {
		GRPCStatusCode string `json:"grpc_status_code,omitempty"`
		StatusCode     string `json:"status_code"`
		Type           string `json:"type,omitempty"`
	}
	p := BlockActionParams{
		StatusCode: 403,
		Type:       "auto",
	}
	var strParams blockActionParams
	var err error
	data, err := json.Marshal(params)
	if err != nil {
		return p, err
	}
	if err := json.Unmarshal(data, &strParams); err != nil {
		return p, err
	}

	p.Type = strParams.Type

	if p.StatusCode, err = strconv.Atoi(strParams.StatusCode); err != nil {
		return p, err
	}
	if strParams.GRPCStatusCode == "" {
		strParams.GRPCStatusCode = "10"
	}

	grpcCode, err := strconv.Atoi(strParams.GRPCStatusCode)
	if err == nil {
		p.GRPCStatusCode = &grpcCode
	}
	return p, err

}

// RedirectParamsFromMap fills a RedirectActionParams struct from the the map returned by the WAF
// for a "redirect_request" action type. This map currently maps all param values to string which
// is why we first peform a decoding to string, before converting.
// Future WAF version may get rid of this string-only mapping, which would in turn make this process
// a lot simpler
func RedirectParamsFromMap(params map[string]any) (RedirectActionParams, error) {
	type redirectActionParams struct {
		Location   string `json:"location,omitempty"`
		StatusCode string `json:"status_code"`
	}
	p := RedirectActionParams{}
	var strParams redirectActionParams
	var err error
	data, err := json.Marshal(params)
	if err != nil {
		return p, err
	}
	if err := json.Unmarshal(data, &strParams); err != nil {
		return p, err
	}

	p.Location = strParams.Location
	p.StatusCode, err = strconv.Atoi(strParams.StatusCode)
	return p, err
}
