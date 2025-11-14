// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

//go:build linux

package internal

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func getContextFromMapping(fields []string) []byte {
	if fields[1] != "r--p" || fields[4] != "0" || fields[3] != "00:00" {
		return nil
	}

	addrs := strings.SplitN(fields[0], "-", 2)
	if len(addrs) < 2 {
		return nil
	}
	vaddr, err := strconv.ParseUint(addrs[0], 16, 64)
	if err != nil {
		return nil
	}

	vend, err := strconv.ParseUint(addrs[1], 16, 64)
	if err != nil {
		return nil
	}

	length := vend - vaddr
	if length != uint64(otelContextMappingSize) {
		return nil
	}

	header := (*processContextHeader)(unsafe.Pointer(uintptr(vaddr)))
	if string(header.Signature[:]) != otelContextSignature {
		return nil
	}
	if header.Version != 1 {
		return nil
	}

	payload := make([]byte, header.PayloadSize)
	copy(payload, unsafe.Slice((*byte)(unsafe.Pointer(header.PayloadAddr)), header.PayloadSize))
	return payload
}

func getContextMapping(mapsFile io.Reader, useMappingNames bool) ([]byte, error) {
	scanner := bufio.NewScanner(mapsFile)
	for scanner.Scan() {

		line := scanner.Text()
		fields := strings.Fields(line)
		if len(fields) < 6 {
			continue
		}

		if (useMappingNames && fields[5] != "[anon:OTEL_CTX]") || (!useMappingNames && fields[5] != "") {
			continue
		}

		payload := getContextFromMapping(fields)
		if payload != nil {
			return payload, nil
		}

		if useMappingNames {
			// When using mapping names, we can stop after the first match.
			break
		}
	}
	return nil, errors.New("no context mapping found")
}

func readProcessLevelContext(useMappingNames bool) ([]byte, error) {
	mapsFile, err := os.Open("/proc/self/maps")
	if err != nil {
		return nil, err
	}
	defer mapsFile.Close()

	return getContextMapping(mapsFile, useMappingNames)
}

func kernelSupportsNamedAnonymousMappings() (bool, error) {
	var uname unix.Utsname
	if err := unix.Uname(&uname); err != nil {
		return false, fmt.Errorf("could not get Kernel Version: %v", err)
	}
	var major, minor, patch uint32
	_, _ = fmt.Fscanf(bytes.NewReader(uname.Release[:]), "%d.%d.%d", &major, &minor, &patch)

	return major > 5 || (major == 5 && minor >= 17), nil
}

func TestCreateOtelProcessContextMapping(t *testing.T) {
	RemoveOtelProcessContextMapping()
	t.Cleanup(func() {
		RemoveOtelProcessContextMapping()
	})

	payload := []byte("hello world")
	err := CreateOtelProcessContextMapping(payload)
	require.NoError(t, err)

	supportsNamedAnonymousMappings, err := kernelSupportsNamedAnonymousMappings()
	require.NoError(t, err)

	ctx, err := readProcessLevelContext(supportsNamedAnonymousMappings)
	require.NoError(t, err)
	require.Equal(t, payload, ctx)
}

func TestCreateOtelProcessContextMappingRejectsOversizedPayload(t *testing.T) {
	RemoveOtelProcessContextMapping()
	t.Cleanup(func() {
		RemoveOtelProcessContextMapping()
	})

	headerSize := int(unsafe.Sizeof(processContextHeader{}))
	oversizedPayload := make([]byte, otelContextMappingSize-headerSize+1)

	err := CreateOtelProcessContextMapping(oversizedPayload)
	require.Error(t, err)
}
