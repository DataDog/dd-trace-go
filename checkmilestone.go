// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2020 Datadog, Inc.

// +build ignore

// This tool validates that the PR at the given URL has a milestone set.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
)

func main() {
	prURL, ok := os.LookupEnv("CIRCLE_PULL_REQUEST")
	if !ok {
		fmt.Println("CIRCLE_PULL_REQUEST not set")
		os.Exit(0)
	}
	exit := func(err error) {
		fmt.Println(err)
		os.Exit(1)
	}
	u, err := url.Parse(prURL)
	if err != nil {
		exit(err)
	}
	base := path.Base(u.Path)
	pr, err := strconv.Atoi(base)
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
	}
}
