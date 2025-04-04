// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package protobuf

import (
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestExtractSchema(t *testing.T) {
	m := FixtureMessage{}
	schema, name, err := getSchema(&m)
	assert.Nil(t, err)
	assert.Equal(t, "protobuf.FixtureMessage", name)
	assert.Equal(t, "{\"messages\":[{\"full_name\":\"protobuf.FixtureMessage.TagsEntry\",\"fields\":[{\"name\":\"key\",\"number\":1,\"cardinality\":\"optional\",\"kind\":\"string\"},{\"name\":\"value\",\"number\":2,\"cardinality\":\"optional\",\"kind\":\"string\"}],\"syntax\":\"proto3\"},{\"full_name\":\"protobuf.FixtureSubMessage\",\"fields\":[{\"name\":\"state\",\"number\":1,\"cardinality\":\"optional\",\"kind\":\"enum\",\"enum\":\"protobuf.FixtureSubMessage.State\"}],\"syntax\":\"proto3\"},{\"full_name\":\"protobuf.FixtureMessage\",\"fields\":[{\"name\":\"name\",\"number\":1,\"cardinality\":\"optional\",\"kind\":\"string\"},{\"name\":\"flags\",\"number\":2,\"cardinality\":\"repeated\",\"kind\":\"bool\"},{\"name\":\"count\",\"number\":3,\"cardinality\":\"optional\",\"kind\":\"int32\"},{\"name\":\"tags\",\"number\":4,\"cardinality\":\"repeated\",\"kind\":\"message\",\"message\":\"protobuf.FixtureMessage.TagsEntry\"},{\"name\":\"sub_message\",\"number\":5,\"cardinality\":\"optional\",\"kind\":\"message\",\"message\":\"protobuf.FixtureSubMessage\"}],\"syntax\":\"proto3\"}],\"enums\":[{\"name\":\"protobuf.FixtureSubMessage.State\",\"values\":[{\"name\":\"Unset\",\"number\":0},{\"name\":\"Ready\",\"number\":1},{\"name\":\"Paused\",\"number\":2}]}],\"parent_message\":\"protobuf.FixtureMessage\",\"kind\":\"protobuf\"}", schema)
}
