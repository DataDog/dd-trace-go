// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

//go:build ignore

// msgp_span_meta_omitempty patches the generated span_msgp.go to add an
// omitempty guard for the spanMeta field.
//
// msgp does not emit omitempty checks for types implementing msgp.Encodable,
// even when the struct tag says ",omitempty". This script injects the
// z.meta.IsZero() guard that the generator would produce for a plain struct
// with an IsZero method.
//
// Usage (in go:generate directives, chained after msgp):
//
//	//go:generate go run ../../scripts/msgp_span_meta_omitempty.go -file span_msgp.go
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
)

func main() {
	fileName := flag.String("file", "", "generated file to process")
	flag.Parse()

	if *fileName == "" {
		fmt.Fprintf(os.Stderr, "usage: msgp_span_meta_omitempty -file <file.go>\n")
		os.Exit(1)
	}

	content, err := os.ReadFile(*fileName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error reading %s: %v\n", *fileName, err)
		os.Exit(1)
	}

	src := string(content)

	// --- Patch EncodeMsg: wrap the "write meta" block with an omitempty guard ---
	// The generator produces:
	//   // write "meta"
	//   err = en.Append(0xa4, 0x6d, 0x65, 0x74, 0x61)
	//   ...
	//   err = z.meta.EncodeMsg(en)
	//   ...
	//   // write "meta_struct"
	//
	// We wrap it with:
	//   if !z.meta.IsZero() {
	//     ...
	//   }
	//
	// And decrement the field count when meta is empty.

	// 1. Add the IsZero field-count decrement in EncodeMsg's "check for omitted fields" block.
	//    Target the EncodeMsg context by matching the tab-level: "\n\tif z.metrics == nil {"
	//    (DecodeMsg has deeper indentation for its metrics nil check.)
	metricsOmit := "\n\tif z.metrics == nil {"
	metaOmit := "\n\tif z.meta.IsZero() {\n\t\tzb0001Len--\n\t\tzb0001Mask |= 0x40\n\t}"
	if !strings.Contains(src, "z.meta.IsZero()") {
		src = strings.Replace(src, metricsOmit, metaOmit+metricsOmit, 1)
	}

	// 2. Wrap the "write meta" block with the omitempty guard.
	writeMetaMarker := "\t\t// write \"meta\"\n"
	writeMetaStructMarker := "\t\t// write \"meta_struct\"\n"
	if !strings.Contains(src, "(zb0001Mask & 0x40)") {
		metaStart := strings.Index(src, writeMetaMarker)
		metaStructStart := strings.Index(src, writeMetaStructMarker)
		if metaStart >= 0 && metaStructStart > metaStart {
			// Extract the meta block
			metaBlock := src[metaStart:metaStructStart]
			// Indent it one more tab level and wrap
			indented := strings.ReplaceAll(metaBlock, "\n\t\t", "\n\t\t\t")
			wrapped := "\t\tif (zb0001Mask & 0x40) == 0 { // if not omitted\n\t" + indented + "\t\t}\n"
			src = src[:metaStart] + wrapped + src[metaStructStart:]
		}
	}

	if err := os.WriteFile(*fileName, []byte(src), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "error writing %s: %v\n", *fileName, err)
		os.Exit(1)
	}
}
