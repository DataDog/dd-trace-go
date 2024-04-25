package actions

import (
	"encoding/json"
	"strconv"
)

type (
	ActionEntry[T any] struct {
		ID         string `json:"id"`
		Type       string `json:"type"`
		Parameters T      `json:"parameters"`
	}

	BlockActionParams struct {
		GRPCStatusCode *int   `json:"grpc_status_code,omitempty"`
		StatusCode     int    `json:"status_code"`
		Type           string `json:"type,omitempty"`
	}
	RedirectActionParams struct {
		Location   string `json:"location,omitempty"`
		StatusCode int    `json:"status_code"`
	}
)

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
