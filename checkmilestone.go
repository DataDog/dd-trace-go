// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

//go:build ignore
// +build ignore

// This tool validates that the PR at the given URL has a milestone set.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
)

func main() {
	exit := func(err error) {
		fmt.Println(err)
		os.Exit(1)
	}
	pr, err := strconv.Atoi(os.Getenv("PR_NUMBER"))
	if err != nil {
		exit(err)
	}
	resp, err := http.Get(fmt.Sprintf("https://api.github.com/repos/DataDog/dd-trace-go/issues/%d", pr))
	if err != nil {
		exit(err)
	}
	var data struct {
		Milestone interface{} `json:"milestone"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		exit(err)
	}
	resp.Body.Close()
	if data.Milestone == nil {
		exit(errors.New("Milestone not set."))
	} else if m, ok := data.Milestone.(map[string]interface{}); ok {
		title, ok := m["title"].(string)
		if !ok {
			exit(errors.New("Could not find milestone \"title\" in milestone map."))
		}
		if strings.ToLower(title) == "triage" {
			exit(errors.New("PR's in the Triage milestone cannot be merged."))
		}
	} else {
		exit(errors.New("Could not resolve milestone. checkmilestone.go likely needs to be updated."))
	}
	fmt.Printf("Milestone check passed for [%v].", data.Milestone)
}
