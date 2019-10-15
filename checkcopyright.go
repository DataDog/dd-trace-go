// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2019 Datadog, Inc.

// +build ignore

// This tool validates that all *.go files in the repository have the copyright text attached.
package main

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func main() {
	var missing bool
	copyrightText := []byte(fmt.Sprintf("// Copyright 2016-%s Datadog, Inc.", time.Now().Format("2006")))
	if err := filepath.Walk(".", func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if filepath.Ext(path) != ".go" || info.IsDir() || strings.Contains(path, "vendor") {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		// read 1KB, header should be there
		snip := make([]byte, 1024)
		_, err = f.Read(snip)
		if err != nil && err != io.EOF {
			return err
		}
		if !bytes.Contains(snip, copyrightText) {
			// report missing header
			missing = true
			log.Printf("Copyright header missing in %q.\n", path)
		}
		return nil
	}); err != nil {
		log.Fatal(err)
	}
	if missing {
		// some files are missing the header, exit code 1 to fail CI
		os.Exit(1)
	}
}
