// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package dsminterface

import (
	"context"
	"time"
)

type Processor interface {
	SetCheckpoint(ctx context.Context, edgeTags ...string) (Pathway, context.Context)
	TrackKafkaProduceOffset(topic string, partition int32, offset int64)
	TrackKafkaCommitOffset(group string, topic string, partition int32, offset int64)
	Flush()
	Stop()
	Start()
}

type Pathway interface {
	GetHash() uint64
	PathwayStart() time.Time
	EdgeStart() time.Time
	Encode() []byte
	EncodeStr() string
}
