// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package main

import (
	"fmt"
	"log"
	"os"
	"regexp"
)

func main() {
	if len(os.Args) < 2 {
		log.Fatalf("Usage: %s \"<text to parse>\"", os.Args[0])
		return
	}

	re := regexp.MustCompile(`<!--({.*})-->`)
	text := os.Args[1]
	matches := re.FindStringSubmatch(text)
	if len(matches) <= 1 {
		return
	}

	fmt.Println(matches[1])
}
