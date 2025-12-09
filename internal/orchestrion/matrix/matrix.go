// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"regexp"
	"slices"
)

// This program lists available releases of `go` to determine the various labels
// to be provided as `actions/setup-go`'s `go-version` input. It outputs a JSON
// encoded array that always includes `oldstable` and `stable`, and may include
// a release candidate version if there is one that is newer than `stable`.
func main() {
	cmd := exec.Command("go", "list", "-m", "-versions", "-json=Versions", "go")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout

	if err := cmd.Run(); err != nil {
		log.Fatalln(err)
	}

	var module struct {
		Versions []string `json:"Versions"`
	}
	dec := json.NewDecoder(&stdout)
	if err := dec.Decode(&module); err != nil {
		log.Fatalln(err)
	}

	if rest, err := io.ReadAll(&stdout); err != nil {
		log.Fatalln(err)
	} else if rest = bytes.TrimSpace(rest); len(rest) > 0 {
		log.Fatalln("Unexpected trailing data in go list output:", string(rest))
	}

	if len(module.Versions) == 0 {
		log.Fatalln("No versions found!")
	}

	versions := append(make([]string, 0, 3), "oldstable", "stable")

	// module.Versions is sorted in semantic versioning order...
	slices.Reverse(module.Versions)
	versionRe := regexp.MustCompile(`^(1\.\d+)(\.\d+)?(?:(beta|rc)(\d+))?$`)
	for _, v := range module.Versions {
		parts := versionRe.FindStringSubmatch(v)
		if parts == nil {
			log.Fatalln("Unsupported version string:", v)
		}
		major, minor, pre, serial := parts[1], parts[2], parts[3], parts[4]
		if pre == "" {
			// Not a pre-release, we're done looking for a "next" release!
			break
		}
		if pre != "rc" {
			// Not a release candidate, we don't test against those...
			continue
		}
		if minor == "" {
			minor = ".0"
		}
		versions = append(versions, major+minor+"-"+pre+"."+serial)
		// We have found a release candidate to test against, we're done!
		break
	}

	jsonText, err := json.Marshal(versions)
	if err != nil {
		log.Fatalln(err)
	}

	if _, err := fmt.Fprintln(os.Stdout, string(jsonText)); err != nil {
		log.Fatalln(err)
	}
}
