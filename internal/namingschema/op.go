// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package namingschema

import (
	"github.com/DataDog/dd-trace-go/v2/v1internal/namingschema"
)

type IntegrationType int

const (
	// client/server
	HTTPClient IntegrationType = iota
	HTTPServer
	GRPCClient
	GRPCServer
	GraphqlServer
	TwirpClient
	TwirpServer

	// messaging
	KafkaOutbound
	KafkaInbound
	GCPPubSubInbound
	GCPPubSubOutbound

	// cache
	MemcachedOutbound
	RedisOutbound

	// db
	ElasticSearchOutbound
	MongoDBOutbound
	CassandraOutbound
	LevelDBOutbound
	BuntDBOutbound
	ConsulOutbound
	VaultOutbound
)

func OpName(t IntegrationType) string {
	return namingschema.OpName(namingschema.IntegrationType(t))
}

func OpNameOverrideV0(t IntegrationType, overrideV0 string) string {
	return namingschema.OpNameOverrideV0(namingschema.IntegrationType(t), overrideV0)
}

func DBOpName(system string, overrideV0 string) string {
	return namingschema.DBOpName(system, overrideV0)
}

func AWSOpName(awsService, awsOp, overrideV0 string) string {
	return namingschema.AWSOpName(awsService, awsOp, overrideV0)
}
