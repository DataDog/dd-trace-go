// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package profiler

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"testing"
	"time"

	kgzip "github.com/klauspost/compress/gzip"
	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/require"
)

func TestNewCompressionPipeline(t *testing.T) {
	plainData := []byte("hello world")
	gzip1Data := compressData(t, plainData, gzip1Compression)
	gzip6Data := compressData(t, plainData, gzip6Compression)
	zstdData := compressData(t, plainData, zstdCompression)

	tests := []struct {
		in   compression
		out  compression
		data []byte
		want []byte
	}{
		{noCompression, gzip1Compression, plainData, gzip1Data},
		{noCompression, gzip6Compression, plainData, gzip6Data},
		{noCompression, zstdCompression, plainData, zstdData},
		{noCompression, noCompression, plainData, plainData},
		{gzip1Compression, gzip1Compression, gzip1Data, gzip1Data},
		{gzip6Compression, gzip6Compression, gzip6Data, gzip6Data},
		{gzip1Compression, zstdCompression, gzip1Data, zstdData},
		{gzip6Compression, zstdCompression, gzip6Data, zstdData},
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

// checkZstdLevel checks that data is zstd-compressed with the given level
func checkZstdLevel(t *testing.T, data []byte, level zstd.EncoderLevel) {
	t.Helper()
	require.NotEmpty(t, data)
	zr, err := zstd.NewReader(bytes.NewReader(data))
	require.NoError(t, err)
	in := new(bytes.Buffer)
	_, err = io.Copy(in, zr)
	require.NoError(t, err)
	out := new(bytes.Buffer)
	zw, err := zstd.NewWriter(out, zstd.WithEncoderLevel(level))
	require.NoError(t, err)
	_, err = io.Copy(zw, in)
	require.NoError(t, err)
	require.NoError(t, zw.Close())
	require.Equal(t, data, out.Bytes())
}

func TestDebugCompressionEnv(t *testing.T) {
	t.Skip("Flaky. See #3681")
	mustGzipDecompress := func(t *testing.T, b []byte) {
		t.Helper()
		r, err := gzip.NewReader(bytes.NewReader(b))
		require.NoError(t, err)
		_, err = io.Copy(io.Discard, r)
		require.NoError(t, err)
	}

	t.Run("default", func(t *testing.T) {
		p := <-startTestProfiler(t, 1, WithDeltaProfiles(false), WithProfileTypes(CPUProfile, HeapProfile, BlockProfile), WithPeriod(time.Millisecond))
		mustGzipDecompress(t, p.attachments["cpu.pprof"])
		mustGzipDecompress(t, p.attachments["heap.pprof"])
		mustGzipDecompress(t, p.attachments["block.pprof"])
	})

	t.Run("explicit-gzip", func(t *testing.T) {
		t.Setenv("DD_PROFILING_DEBUG_COMPRESSION_SETTINGS", "gzip")
		p := <-startTestProfiler(t, 1, WithProfileTypes(HeapProfile, BlockProfile), WithPeriod(time.Millisecond))
		mustGzipDecompress(t, p.attachments["delta-heap.pprof"])
	})

	t.Run("zstd-delta", func(t *testing.T) {
		t.Setenv("DD_PROFILING_DEBUG_COMPRESSION_SETTINGS", "zstd-3")
		p := <-startTestProfiler(t, 1, WithProfileTypes(CPUProfile, HeapProfile), WithPeriod(time.Millisecond))
		checkZstdLevel(t, p.attachments["cpu.pprof"], zstd.SpeedBetterCompression)
		checkZstdLevel(t, p.attachments["delta-heap.pprof"], zstd.SpeedBetterCompression)
	})

	t.Run("zstd-no-delta", func(t *testing.T) {
		t.Setenv("DD_PROFILING_DEBUG_COMPRESSION_SETTINGS", "zstd-3")
		p := <-startTestProfiler(t, 1, WithDeltaProfiles(false), WithProfileTypes(HeapProfile), WithPeriod(time.Millisecond))
		checkZstdLevel(t, p.attachments["heap.pprof"], zstd.SpeedBetterCompression)
	})

	t.Run("zstd-2", func(t *testing.T) {
		t.Setenv("DD_PROFILING_DEBUG_COMPRESSION_SETTINGS", "zstd-2")
		p := <-startTestProfiler(t, 1, WithProfileTypes(HeapProfile), WithPeriod(time.Millisecond))
		checkZstdLevel(t, p.attachments["delta-heap.pprof"], zstd.SpeedDefault)
	})

	t.Run("zstd-no-level", func(t *testing.T) {
		t.Setenv("DD_PROFILING_DEBUG_COMPRESSION_SETTINGS", "zstd")
		p := <-startTestProfiler(t, 1, WithProfileTypes(CPUProfile, HeapProfile), WithPeriod(time.Millisecond))
		checkZstdLevel(t, p.attachments["cpu.pprof"], zstd.SpeedDefault)
		checkZstdLevel(t, p.attachments["delta-heap.pprof"], zstd.SpeedDefault)
	})
}

func compressData(t testing.TB, data []byte, c compression) []byte {
	t.Helper()
	buf := &bytes.Buffer{}
	switch c.algorithm {
	case compressionAlgorithmGzip:
		// NB: we need to use the same gzip implementation here as we do
		// in the compressor since "level" isn't actually a stable thing
		// across implementations
		gw, err := kgzip.NewWriterLevel(buf, c.level)
		require.NoError(t, err)
		_, err = gw.Write(data)
		require.NoError(t, err)
		require.NoError(t, gw.Close())
	case compressionAlgorithmZstd:
		level := zstd.SpeedDefault
		if l, ok := zstdLevels[c.level]; ok {
			level = l
		}
		zw, err := zstd.NewWriter(buf, zstd.WithEncoderLevel(level))
		require.NoError(t, err)
		_, err = zw.Write(data)
		require.NoError(t, err)
		require.NoError(t, zw.Close())
	default:
		t.Fatalf("unsupported compression algorithm: %s", c.algorithm)
	}
	return buf.Bytes()
}

func BenchmarkRecompression(b *testing.B) {
	inputdata, err := os.ReadFile("internal/fastdelta/testdata/big-heap.pprof")
	if err != nil {
		b.Fatal(err)
	}
	inputs := []struct {
		inAlg    compression
		outLevel zstd.EncoderLevel
	}{
		{gzip1Compression, zstd.SpeedDefault},
		{gzip1Compression, zstd.SpeedBetterCompression},
		{gzip1Compression, zstd.SpeedBestCompression},
		{gzip6Compression, zstd.SpeedDefault},
		{gzip6Compression, zstd.SpeedBetterCompression},
		{gzip6Compression, zstd.SpeedBestCompression},
	}
	for _, in := range inputs {
		b.Run(fmt.Sprintf("%s-%s", in.inAlg.String(), in.outLevel), func(b *testing.B) {
			data := compressData(b, inputdata, in.inAlg)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				z, err := newZstdRecompressor(in.outLevel)
				if err != nil {
					b.Fatal(err)
				}
				z.Reset(io.Discard)
				if _, err := z.Write(data); err != nil {
					b.Fatal(err)
				}
				if err := z.Close(); err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
			b.ReportMetric(
				(float64(b.N*len(data))/(1024*1024))/
					(float64(b.Elapsed().Seconds())),
				"MiB/s",
			)
		})
	}

	for _, in := range inputs {
		b.Run(fmt.Sprintf("decomp-recomp-%s-%s", in.inAlg.String(), in.outLevel), func(b *testing.B) {
			// For comparison with the pipe-based recompressor, to
			// see if it's any faster to just decompress and
			// recompress serially
			data := compressData(b, inputdata, in.inAlg)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				buf := new(bytes.Buffer)
				gr, err := kgzip.NewReader(bytes.NewReader(data))
				if err != nil {
					b.Fatal(err)
				}
				io.Copy(buf, gr)
				zw, err := zstd.NewWriter(io.Discard, zstd.WithEncoderLevel(in.outLevel))
				if err != nil {
					b.Fatal(err)
				}
				io.Copy(zw, buf)
				zw.Close()
			}
			b.StopTimer()
			b.ReportMetric(
				(float64(b.N*len(data))/(1024*1024))/
					(float64(b.Elapsed().Seconds())),
				"MiB/s",
			)
		})
	}

	for _, level := range []zstd.EncoderLevel{zstd.SpeedDefault, zstd.SpeedBetterCompression, zstd.SpeedBestCompression} {
		b.Run(fmt.Sprintf("no-compression-zstd-%s", level), func(b *testing.B) {
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				zw, err := zstd.NewWriter(io.Discard, zstd.WithEncoderLevel(level))
				if err != nil {
					b.Fatal(err)
				}
				zw.Write(inputdata)
				zw.Close()
			}
			b.StopTimer()
			b.ReportMetric(
				(float64(b.N*len(inputdata))/(1024*1024))/
					(float64(b.Elapsed().Seconds())),
				"MiB/s",
			)
		})
	}
}
