// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package coverage

import (
	"errors"
	"math"
	"strconv"
)

const (
	// lineCoverageMetaVersion is the version of the block table encoding.
	// Version 1 has no credited lines, versions before 3 have one-byte
	// counters, and versions before 4 have no init lines.
	lineCoverageMetaVersion = 4
	// lineCoverageBlockFlagInit marks the blocks of the body of a `func init()`.
	lineCoverageBlockFlagInit = 1
	// lineCoverageBlockFlagLines marks blocks that credit lines outside their
	// range.
	lineCoverageBlockFlagLines = 2
	// lineCoverageBlockFlagNoRange marks blocks without lines of their own.
	lineCoverageBlockFlagNoRange = 4
)

var errInvalidLineCoverageMeta = errors.New("invalid line coverage block table")

// lineCoverageBlock is one block of an instrumented package. A block with no
// lines has startLine > endLine.
type lineCoverageBlock struct {
	file      int32 // index of the collector's file
	startLine int32
	endLine   int32
	isInit    bool
	// lines are lines of the file the block credits besides its range: the
	// header of the function it starts, case lines, or the closing brace of
	// the function it ends. They count only where the line table maps code to
	// them.
	lines []int32
}

// lineCoverageTable is the decoded block table of an instrumented package.
type lineCoverageTable struct {
	blocks      []lineCoverageBlock
	counterSize int // in bytes
	// initLines are, by collector file, the header lines of the function
	// literals in package-level variable initialisers: their func values are
	// built at package initialization.
	initLines map[int32][]int32
}

// decodeLineCoverageMeta decodes the block table that orchestrion embeds in
// each instrumented package:
//
//	uvarint version
//	uvarint counter size in bytes, 1 or 4 (from version 3)
//	uvarint file count, then for each file: uvarint length, bytes
//	uvarint block count, then for each block:
//	  uvarint file index, uvarint start line, uvarint end line - start line, byte flags
//	  with lineCoverageBlockFlagLines: uvarint line count, then each line as a uvarint
//	from version 4, uvarint count of files with init lines, then for each:
//	  uvarint file index, uvarint line count, then each line as a uvarint
//
// fileIndex maps a file name of the table to an index of the collector's files.
func decodeLineCoverageMeta(meta string, numBlocks int, fileIndex func(name string) int32) (lineCoverageTable, error) {
	r := metaReader{data: meta}
	version := r.uvarint()
	if r.err == nil && (version == 0 || version > lineCoverageMetaVersion) {
		return lineCoverageTable{}, errors.New("unsupported line coverage block table version " + strconv.FormatUint(version, 10))
	}
	table := lineCoverageTable{counterSize: 1}
	if version >= 3 {
		switch size := r.uvarint(); size {
		case 1, 4:
			table.counterSize = int(size)
		default:
			return lineCoverageTable{}, errInvalidLineCoverageMeta
		}
	}
	fileCount := r.uvarint()
	if r.err != nil || fileCount > uint64(len(meta)) {
		return lineCoverageTable{}, errInvalidLineCoverageMeta
	}
	files := make([]int32, fileCount)
	for i := range files {
		name := r.string(r.uvarint())
		if r.err != nil {
			return lineCoverageTable{}, r.err
		}
		files[i] = fileIndex(name)
	}
	if blockCount := r.uvarint(); r.err != nil || blockCount != uint64(numBlocks) {
		return lineCoverageTable{}, errInvalidLineCoverageMeta
	}
	blocks := make([]lineCoverageBlock, numBlocks)
	for i := range blocks {
		file, start, span, flags := r.uvarint(), r.uvarint(), r.uvarint(), r.byte()
		if r.err != nil || file >= fileCount || start > math.MaxInt32 || span > math.MaxInt32-start {
			return lineCoverageTable{}, errInvalidLineCoverageMeta
		}
		b := lineCoverageBlock{
			file:      files[file],
			startLine: int32(start),
			endLine:   int32(start + span),
			isInit:    flags&lineCoverageBlockFlagInit != 0,
		}
		if b.startLine == 0 || (version > 1 && flags&lineCoverageBlockFlagNoRange != 0) {
			b.startLine, b.endLine = 1, 0
		}
		if version > 1 && flags&lineCoverageBlockFlagLines != 0 {
			if b.lines = r.lines(); r.err != nil {
				return lineCoverageTable{}, r.err
			}
		}
		blocks[i] = b
	}
	table.blocks = blocks
	if version >= 4 {
		count := r.uvarint()
		if r.err != nil || count > fileCount {
			return lineCoverageTable{}, errInvalidLineCoverageMeta
		}
		for range count {
			file := r.uvarint()
			if r.err != nil || file >= fileCount {
				return lineCoverageTable{}, errInvalidLineCoverageMeta
			}
			lines := r.lines()
			if r.err != nil {
				return lineCoverageTable{}, r.err
			}
			if table.initLines == nil {
				table.initLines = map[int32][]int32{}
			}
			table.initLines[files[file]] = append(table.initLines[files[file]], lines...)
		}
	}
	if r.pos != len(r.data) {
		return lineCoverageTable{}, errInvalidLineCoverageMeta
	}
	return table, nil
}

// lines reads a line count, then that many lines.
func (r *metaReader) lines() []int32 {
	count := r.uvarint()
	if r.err != nil || count > uint64(len(r.data)) {
		r.err = errInvalidLineCoverageMeta
		return nil
	}
	lines := make([]int32, count)
	for i := range lines {
		line := r.uvarint()
		if r.err != nil || line == 0 || line > math.MaxInt32 {
			r.err = errInvalidLineCoverageMeta
			return nil
		}
		lines[i] = int32(line)
	}
	return lines
}

// metaReader reads a block table. Its first error sticks.
type metaReader struct {
	data string
	pos  int
	err  error
}

func (r *metaReader) uvarint() uint64 {
	var x uint64
	for shift := uint(0); shift < 64; shift += 7 {
		b := r.byte()
		if r.err != nil {
			return 0
		}
		if b < 0x80 {
			if shift == 63 && b > 1 {
				break
			}
			return x | uint64(b)<<shift
		}
		x |= uint64(b&0x7f) << shift
	}
	r.err = errInvalidLineCoverageMeta
	return 0
}

func (r *metaReader) byte() byte {
	if r.err != nil {
		return 0
	}
	if r.pos >= len(r.data) {
		r.err = errInvalidLineCoverageMeta
		return 0
	}
	b := r.data[r.pos]
	r.pos++
	return b
}

func (r *metaReader) string(n uint64) string {
	if r.err != nil {
		return ""
	}
	if n > uint64(len(r.data)-r.pos) {
		r.err = errInvalidLineCoverageMeta
		return ""
	}
	s := r.data[r.pos : r.pos+int(n)]
	r.pos += int(n)
	return s
}
