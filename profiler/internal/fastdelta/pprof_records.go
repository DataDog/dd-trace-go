// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package fastdelta

// enum types for pprof protobuf records
// see https://github.com/google/pprof/blob/main/proto/profile.proto
// note: avoid iota, hard-code the constants, so they are not order-sensitive

// ProfileRecordNumber type for Profile message records
type ProfileRecordNumber int32

const (
	recProfileSampleType        ProfileRecordNumber = 1
	recProfileSample            ProfileRecordNumber = 2
	recProfileMapping           ProfileRecordNumber = 3
	recProfileLocation          ProfileRecordNumber = 4
	recProfileFunction          ProfileRecordNumber = 5
	recProfileStringTable       ProfileRecordNumber = 6
	recProfileDropFrames        ProfileRecordNumber = 7
	recProfileKeepFrames        ProfileRecordNumber = 8
	recProfileTimeNanos         ProfileRecordNumber = 9
	recProfileDurationNanos     ProfileRecordNumber = 10
	recProfilePeriodType        ProfileRecordNumber = 11
	recProfilePeriod            ProfileRecordNumber = 12
	recProfileComment           ProfileRecordNumber = 13
	recProfileDefaultSampleType ProfileRecordNumber = 14
)

// ValueTypeRecordNumber type for ValueType message records
type ValueTypeRecordNumber int32

const (
	recValueTypeType ValueTypeRecordNumber = 1
	recValueTypeUnit ValueTypeRecordNumber = 2
)

// SampleRecordNumber type for Sample message records
type SampleRecordNumber int32

const (
	recSampleLocationID SampleRecordNumber = 1
	recSampleValue      SampleRecordNumber = 2
	recSampleLabel      SampleRecordNumber = 3
)

// LabelRecordNumber type for Label message records
type LabelRecordNumber int32

const (
	recLabelKey     LabelRecordNumber = 1
	recLabelStr     LabelRecordNumber = 2
	recLabelNum     LabelRecordNumber = 3
	recLabelNumUnit LabelRecordNumber = 4
)

// MappingRecordNumber type for Mapping message records
type MappingRecordNumber int32

const (
	recMappingID              MappingRecordNumber = 1
	recMappingMemoryStart     MappingRecordNumber = 2
	recMappingMemoryLimit     MappingRecordNumber = 3
	recMappingFileOffset      MappingRecordNumber = 4
	recMappingFilename        MappingRecordNumber = 5
	recMappingBuildID         MappingRecordNumber = 6
	recMappingHasFunctions    MappingRecordNumber = 7
	recMappingHasFilenames    MappingRecordNumber = 8
	recMappingHasLineNumbers  MappingRecordNumber = 9
	recMappingHasInlineFrames MappingRecordNumber = 10
)

// LocationRecordNumber type for Location message records
type LocationRecordNumber int32

const (
	recLocationID        LocationRecordNumber = 1
	recLocationMappingID LocationRecordNumber = 2
	recLocationAddress   LocationRecordNumber = 3
	recLocationLine      LocationRecordNumber = 4
	recLocationIsFolded  LocationRecordNumber = 5
)

// LineRecordNumber type for Line message records
type LineRecordNumber int32

const (
	recLineFunctionID LineRecordNumber = 1
	recLineLine       LineRecordNumber = 2
)

// FunctionRecordNumber type for Function message records
type FunctionRecordNumber int32

const (
	recFunctionID         FunctionRecordNumber = 1
	recFunctionName       FunctionRecordNumber = 2
	recFunctionSystemName FunctionRecordNumber = 3
	recFunctionFilename   FunctionRecordNumber = 4
	recFunctionStartLine  FunctionRecordNumber = 5
)
