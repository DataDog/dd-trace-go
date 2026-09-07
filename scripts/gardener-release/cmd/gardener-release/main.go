// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	gardenerrelease "github.com/DataDog/dd-trace-go/scripts/gardener-release"
)

func main() {
	os.Exit(run(os.Args[1:], gardenerrelease.OSFileSystem{}, os.Stdout, os.Stderr))
}

func run(args []string, files gardenerrelease.FileSystem, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printError(stderr, gardenerrelease.NewCLIError("missing_command"))
		return 2
	}
	switch args[0] {
	case "inspect-request":
		return inspectRequest(args[1:], files, stdout, stderr)
	case "inspect-operation":
		return inspectOperation(args[1:], files, stdout, stderr)
	default:
		printError(stderr, gardenerrelease.NewCLIError("unknown_command"))
		return 2
	}
}

func inspectRequest(args []string, files gardenerrelease.FileSystem, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("inspect-request", flag.ContinueOnError)
	flags.SetOutput(stderr)
	inputPath := flags.String("input", "", "workflow_dispatch input JSON file")
	policyPath := flags.String("policy", "", "reviewed release policy JSON file")
	if err := flags.Parse(args); err != nil {
		printError(stderr, err)
		return 2
	}
	if *inputPath == "" || *policyPath == "" || flags.NArg() != 0 {
		printError(stderr, gardenerrelease.NewCLIError("invalid_arguments"))
		return 2
	}
	inputRaw, err := gardenerrelease.ReadBoundedFile(files, *inputPath, gardenerrelease.MaxContextBytes+1024)
	if err != nil {
		printError(stderr, err)
		return 1
	}
	policyRaw, err := gardenerrelease.ReadBoundedFile(files, *policyPath, gardenerrelease.MaxPolicyBytes)
	if err != nil {
		printError(stderr, err)
		return 1
	}
	inspection, err := gardenerrelease.InspectRequest(inputRaw, policyRaw)
	if err != nil {
		printError(stderr, err)
		return 1
	}
	return writeJSON(stdout, stderr, inspection)
}

func inspectOperation(args []string, files gardenerrelease.FileSystem, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("inspect-operation", flag.ContinueOnError)
	flags.SetOutput(stderr)
	inputPath := flags.String("input", "", "operation evidence JSON file")
	if err := flags.Parse(args); err != nil {
		printError(stderr, err)
		return 2
	}
	if *inputPath == "" || flags.NArg() != 0 {
		printError(stderr, gardenerrelease.NewCLIError("invalid_arguments"))
		return 2
	}
	inputRaw, err := gardenerrelease.ReadBoundedFile(files, *inputPath, gardenerrelease.MaxContextBytes)
	if err != nil {
		printError(stderr, err)
		return 1
	}
	inspection, err := gardenerrelease.InspectOperation(inputRaw)
	if err != nil {
		printError(stderr, err)
		return 1
	}
	return writeJSON(stdout, stderr, inspection)
}

func writeJSON(stdout, stderr io.Writer, value any) int {
	data, err := gardenerrelease.MarshalInspection(value)
	if err != nil {
		printError(stderr, err)
		return 1
	}
	_, _ = stdout.Write(data)
	_, _ = stdout.Write([]byte("\n"))
	return 0
}

func printError(stderr io.Writer, err error) {
	class := gardenerrelease.ClassOf(err)
	payload := map[string]string{"error": gardenerrelease.ErrorCode(err)}
	if class != "" {
		payload["class"] = string(class)
	}
	data, marshalErr := json.Marshal(payload)
	if marshalErr != nil {
		fmt.Fprintln(stderr, `{"error":"internal"}`)
		return
	}
	fmt.Fprintln(stderr, string(data))
}
