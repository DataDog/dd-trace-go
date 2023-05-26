// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package datastreams

import (
	"sync/atomic"
	"time"
)

func TrackKafkaCommitOffset(group string, topic string, partition int32, offset int64) {
	if aggregator := getGlobalAggregator(); aggregator != nil {
		select {
		case aggregator.inKafka <- kafkaOffset{
			offset:     offset,
			group:      group,
			topic:      topic,
			partition:  partition,
			offsetType: commitOffset,
			timestamp:  time.Now().UnixNano(),
		}:
		default:
			atomic.AddInt64(&aggregator.stats.dropped, 1)
		}
	}
}

func TrackKafkaProduce(topic string, partition int32, offset int64) {
	if aggregator := getGlobalAggregator(); aggregator != nil {
		select {
		case aggregator.inKafka <- kafkaOffset{
			offset:     offset,
			topic:      topic,
			partition:  partition,
			offsetType: produceOffset,
			timestamp:  time.Now().UnixNano(),
		}:
		default:
			atomic.AddInt64(&aggregator.stats.dropped, 1)
		}
	}
}
