// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package profiler

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewCompressionPipeline(t *testing.T) {
	plainData := []byte("hello world")
	gzip1Data := compressData(t, plainData, gzip1Compression)
	gzip6Data := compressData(t, plainData, gzip6Compression)

	tests := []struct {
		in   compression
		out  compression
		data []byte
		want []byte
	}{
		{noCompression, gzip1Compression, plainData, gzip1Data},
		{noCompression, gzip6Compression, plainData, gzip6Data},
		{noCompression, noCompression, plainData, plainData},
		{gzip1Compression, gzip1Compression, gzip1Data, gzip1Data},
		{gzip6Compression, gzip6Compression, gzip6Data, gzip6Data},
	}

	for _, test := range tests {
		t.Run(fmt.Sprintf("%s->%s", test.in, test.out), func(t *testing.T) {
			pipeline, err := newCompressionPipeline(test.in, test.out)
			require.NoError(t, err)
			buf := &bytes.Buffer{}
			pipeline.Reset(buf)
			_, err = pipeline.Write(test.data)
			require.NoError(t, err)
			require.NoError(t, pipeline.Close())
			require.Equal(t, test.want, buf.Bytes())
		})
	}
}

func compressData(t *testing.T, data []byte, c compression) []byte {
	t.Helper()
	buf := &bytes.Buffer{}
	switch c.algorithm {
	case compressionAlgorithmGzip:
		gw, err := gzip.NewWriterLevel(buf, c.level)
		require.NoError(t, err)
		_, err = gw.Write(data)
		require.NoError(t, err)
		require.NoError(t, gw.Close())
	default:
		t.Fatalf("unsupported compression algorithm: %s", c.algorithm)
	}
	return buf.Bytes()
}
