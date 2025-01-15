// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	gts "gotest.tools/gotestsum/cmd"
)

// go list ./... | grep -v -e google.golang.org/api -e sarama -e confluent-kafka-go -e cmemprof | sort >packages.txt
// gotestsum --junitfile ${REPORT} -- $(cat packages.txt) -v -coverprofile=coverage.txt -covermode=atomic -timeout 15m

func main() {
	listArgs := "go list ./... | grep -v -e google.golang.org/api -e sarama -e confluent-kafka-go -e cmemprof | sort >packages.txt"
	cmd := exec.Command("bash", "-c", listArgs)
	basepath, _ := os.Getwd()
	root := strings.Split(basepath, ".github")[0]
	cmd.Dir = root
	err := cmd.Run()
	if err != nil {
		fmt.Printf("error building packages.txt: %s\n", err.Error())
	}
	pkgOut, err := exec.Command("cat", "packages.txt").Output()
	if err != nil {
		fmt.Printf("error getting packages.txt: %s\n", err.Error())
	}
	gtsArgs := []string{"--junitfile", "gotestsum-report.xml", "--", string(pkgOut), "-v", "-coverprofile=coverage.txt", "-covermode=atomic", "-timeout 15m"}
	err = gts.Run("gotestsum", gtsArgs)
	if err != nil {
		fmt.Printf("error building junitfile: %s\n", err.Error())
	}

}
