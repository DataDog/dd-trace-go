// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import "testing"

type memoryFS map[string][]byte

func (m memoryFS) ReadFile(name string) ([]byte, error) {
	data, ok := m[name]
	if !ok {
		return nil, &missingFileError{}
	}
	return append([]byte(nil), data...), nil
}

type missingFileError struct{}

func (*missingFileError) Error() string { return "file does not exist" }

func TestReadBoundedFileInjectsFilesystemAndBoundsInput(t *testing.T) {
	files := memoryFS{"input.json": []byte("12345")}
	data, err := ReadBoundedFile(files, "input.json", 5)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "12345" {
		t.Fatalf("data = %q", data)
	}
	_, err = ReadBoundedFile(files, "input.json", 4)
	if ErrorCode(err) != "file_too_large" {
		t.Fatalf("error = %q, want file_too_large", ErrorCode(err))
	}
}
