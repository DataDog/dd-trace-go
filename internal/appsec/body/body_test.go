// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package body

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewEncodable_UnsupportedContentType(t *testing.T) {
	r := io.NopCloser(bytes.NewReader([]byte(`{"key":"value"}`)))
	reader := &r
	enc, err := NewEncodable("text/plain", reader, 100)
	assert.Nil(t, enc)
	assert.NoError(t, err)
}

func TestNewEncodable_NilReader(t *testing.T) {
	var reader *io.ReadCloser
	enc, err := NewEncodable("application/json", reader, 100)
	assert.Nil(t, enc)
	assert.Error(t, err)
}

func TestNewEncodable_ValidJSON(t *testing.T) {
	r := io.NopCloser(bytes.NewReader([]byte(`{"key":"value"}`)))
	reader := &r
	enc, err := NewEncodable("application/json", reader, 100)
	assert.NotNil(t, enc)
	assert.NoError(t, err)
}

func TestNewEncodable_Truncated(t *testing.T) {
	r := io.NopCloser(bytes.NewReader([]byte(`{"key":"value","extra":"data"}`)))
	reader := &r
	enc, err := NewEncodable("application/json", reader, 10)
	assert.NotNil(t, enc)
	assert.NoError(t, err)
}

func TestOriginalReaderIsUsable(t *testing.T) {
	originalData := `{"key":"value","extra":"data"}`
	r := io.NopCloser(strings.NewReader(originalData))
	reader := &r
	enc, err := NewEncodable("application/json", reader, 1000)
	assert.NotNil(t, enc)
	assert.NoError(t, err)

	// Read from the original reader
	data, err := io.ReadAll(*reader)
	assert.NoError(t, err)

	assert.Equal(t, originalData, string(data))
}

func TestOriginalReaderIsUsableLowerLimit(t *testing.T) {
	originalData := `{"key":"value","extra":"data"}`
	r := io.NopCloser(strings.NewReader(originalData))
	reader := &r
	enc, err := NewEncodable("application/json", reader, 10)
	assert.NotNil(t, enc)
	assert.NoError(t, err)

	// Read from the original reader
	data, err := io.ReadAll(*reader)
	assert.NoError(t, err)

	assert.Equal(t, originalData, string(data))
}

func TestNewEncodable_ReadError(t *testing.T) {
	badReader := io.NopCloser(&errorReader{})
	reader := &badReader
	enc, err := NewEncodable("application/json", reader, 100)
	assert.Nil(t, enc)
	assert.Error(t, err)
}

type errorReader struct{}

func (e *errorReader) Read(p []byte) (int, error) {
	return 0, errors.New("read error")
}
