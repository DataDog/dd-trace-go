// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

//go:build linux

package otelprocesscontext

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"os"
	"strconv"
	"strings"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/require"
)

func getContextFromMapping(fields []string) []byte {
	addrs := strings.SplitN(fields[0], "-", 2)
	if len(addrs) < 2 {
		return nil
	}
	vaddr, err := strconv.ParseUint(addrs[0], 16, 64)
	if err != nil {
		return nil
	}

	header := (*processContextHeader)(unsafe.Pointer(uintptr(vaddr)))
	if string(header.Signature[:]) != otelContextSignature {
		return nil
	}
	if header.Version != 2 {
		return nil
	}
	if header.PublishedAtNs == 0 {
		return nil
	}

	payload := make([]byte, header.PayloadSize)
	copy(payload, unsafe.Slice((*byte)(unsafe.Pointer(uintptr(header.PayloadAddr))), header.PayloadSize))
	return payload
}

func isOtelContextName(name string) bool {
	return name == "[anon:OTEL_CTX]" ||
		name == "[anon_shmem:OTEL_CTX]" ||
		strings.HasPrefix(name, "/memfd:OTEL_CTX")
}

func getContextMapping(mapsFile io.Reader) ([]byte, error) {
	content, err := io.ReadAll(mapsFile)
	if err != nil {
		return nil, err
	}

	scanner := bufio.NewScanner(bytes.NewReader(content))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 6 || !isOtelContextName(fields[5]) {
			continue
		}
		if payload := getContextFromMapping(fields); payload != nil {
			return payload, nil
		}
	}

	return nil, errors.New("no context mapping found")
}

func readProcessLevelContext() ([]byte, error) {
	mapsFile, err := os.Open("/proc/self/maps")
	if err != nil {
		return nil, err
	}
	defer mapsFile.Close()

	return getContextMapping(mapsFile)
}

func TestCreateOtelProcessContextMapping(t *testing.T) {
	removeOtelProcessContextMapping()
	t.Cleanup(func() {
		removeOtelProcessContextMapping()
	})

	payload := []byte("hello world")
	err := CreateOtelProcessContextMapping(payload)
	require.NoError(t, err)

	ctx, err := readProcessLevelContext()
	require.NoError(t, err)
	require.Equal(t, payload, ctx)
}

func TestCreateOtelProcessContextMappingRejectsOversizedPayload(t *testing.T) {
	removeOtelProcessContextMapping()
	t.Cleanup(func() {
		removeOtelProcessContextMapping()
	})

	headerSize := int(unsafe.Sizeof(processContextHeader{}))
	oversizedPayload := make([]byte, otelContextMappingSize-headerSize+1)

	err := CreateOtelProcessContextMapping(oversizedPayload)
	require.Error(t, err)
}
