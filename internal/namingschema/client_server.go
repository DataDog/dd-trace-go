// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package namingschema

import "fmt"

type ClientServerSystem string

const (
	ClientServerSystemHTTP    ClientServerSystem = "http"
	ClientServerSystemGraphQL ClientServerSystem = "graphql"
	ClientServerSystemGRPC    ClientServerSystem = "grpc"
	ClientServerSystemTwirp   ClientServerSystem = "twirp"
)

type clientOutboundOperationNameSchema struct {
	system ClientServerSystem
}

func NewClientOutboundOperationNameSchema(system ClientServerSystem) *Schema {
	return New(&clientOutboundOperationNameSchema{system: system})
}

func (c *clientOutboundOperationNameSchema) V0() string {
	return fmt.Sprintf("%s.request", c.system)
}

func (c *clientOutboundOperationNameSchema) V1() string {
	return fmt.Sprintf("%s.client.request", c.system)
}

type serverInboundOperationNameSchema struct {
	system ClientServerSystem
}

func NewServerInboundOperationNameSchema(system ClientServerSystem) *Schema {
	return New(&serverInboundOperationNameSchema{system: system})
}

func (s *serverInboundOperationNameSchema) V0() string {
	return fmt.Sprintf("%s.request", s.system)
}

func (s *serverInboundOperationNameSchema) V1() string {
	return fmt.Sprintf("%s.server.request", s.system)
}
