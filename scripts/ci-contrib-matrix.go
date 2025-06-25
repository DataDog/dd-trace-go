// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

//go:build ignore
// +build ignore

// This tool outputs a JSON encoded array that can be used as a matrix input to GitHub workflows.
// Rather than testing all contribs under one job, we would rather parallelize the jobs
// by using a matrix.
// The `APM Larger Runners` group shares around 50 runners. We should not use all 50.
// TODO: can we find an optimal number of runners that will make the test efficient without
// creating too much cost?
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"regexp"
)

const numRunners = 6

func main() {
	cmd := exec.Command("go", "list", "-m", "-json")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout

	if err := cmd.Run(); err != nil {
		log.Fatalln(err)
	}

	contribs := make([]string, numRunners)
	i := 0
	dec := json.NewDecoder(&stdout)
	for dec.More() {
		var pkg struct {
			Path string `json:"Path"`
		}
		if err := dec.Decode(&pkg); err != nil {
			continue
		}
		// we want to only count packages in the contrib directory
		validContrib := regexp.MustCompile(`/contrib/.*/`).FindStringSubmatch(pkg.Path)
		if validContrib == nil {
			continue
		}
		contribs[i] += "." + validContrib[0] + " "
		i = (i + 1) % numRunners
	}

	jsonText, err := json.Marshal(contribs)
	if err != nil {
		log.Fatalln(err)
	}

	if _, err := fmt.Fprintln(os.Stdout, string(jsonText)); err != nil {
		log.Fatalln(err)
	}
}
