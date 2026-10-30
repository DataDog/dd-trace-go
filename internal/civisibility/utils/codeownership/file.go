// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package codeownership

import (
	"bufio"
	"encoding/binary"
	"io"
	"strings"
	"unicode/utf16"
)

// Decode file encodings before parsing. A BOM is recognized only at file start.
func parseFile(reader io.Reader, dialect Dialect) (*CodeOwners, error) {
	r := bufio.NewReader(reader)
	prefix, _ := r.Peek(4)
	skip, width := 0, 0
	var order binary.ByteOrder = binary.LittleEndian
	switch {
	case len(prefix) >= 4 && string(prefix[:4]) == "\xff\xfe\x00\x00":
		skip, width = 4, 4
	case len(prefix) >= 4 && string(prefix[:4]) == "\x00\x00\xfe\xff":
		skip, width, order = 4, 4, binary.BigEndian
	case len(prefix) >= 3 && string(prefix[:3]) == "\xef\xbb\xbf":
		skip = 3
	case len(prefix) >= 2 && string(prefix[:2]) == "\xff\xfe":
		skip, width = 2, 2
	case len(prefix) >= 2 && string(prefix[:2]) == "\xfe\xff":
		skip, width, order = 2, 2, binary.BigEndian
	}
	r.Discard(skip)
	if width == 0 {
		return Parse(&fileLineReader{reader: r}, dialect)
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	var decoded strings.Builder
	if width == 2 {
		units := make([]uint16, len(data)/2)
		for i := range units {
			units[i] = order.Uint16(data[i*2:])
		}
		for _, char := range utf16.Decode(units) {
			decoded.WriteRune(char)
		}
	} else {
		for len(data) >= 4 {
			decoded.WriteRune(rune(order.Uint32(data[:4])))
			data = data[4:]
		}
	}
	if len(data)%width != 0 {
		decoded.WriteRune('\ufffd')
	}
	return Parse(&fileLineReader{reader: strings.NewReader(decoded.String())}, dialect)
}

// Normalize CR, LF and CRLF, including across reads.
// Translate only file input; Parse accepts the caller's already decoded lines.
type fileLineReader struct {
	reader  io.Reader
	afterCR bool
}

func (r *fileLineReader) Read(buffer []byte) (int, error) {
	if len(buffer) == 0 {
		return 0, nil
	}
	for {
		n, err := r.reader.Read(buffer)
		out := 0
		for _, b := range buffer[:n] {
			if r.afterCR && b == '\n' {
				r.afterCR = false
				continue
			}
			r.afterCR = b == '\r'
			if r.afterCR {
				b = '\n'
			}
			buffer[out] = b
			out++
		}
		if out != 0 || err != nil {
			return out, err
		}
	}
}
