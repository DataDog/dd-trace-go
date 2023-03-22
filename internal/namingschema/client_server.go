// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package namingschema

import "fmt"

// ClientServerSystem represents client-server systems or protocols to be used for client-server naming
// schemas in this package.
type ClientServerSystem string

const (
	// ClientServerSystemHTTP represents HTTP.
	ClientServerSystemHTTP ClientServerSystem = "http"
	// ClientServerSystemGraphQL represents GraphQL.
	ClientServerSystemGraphQL ClientServerSystem = "graphql"
	// ClientServerSystemGRPC represents gRPC RPC system.
	ClientServerSystemGRPC ClientServerSystem = "grpc"
	// ClientServerSystemTwirp represents twirp RPC system.
	ClientServerSystemTwirp ClientServerSystem = "twirp"
)

type clientOutboundOperationNameSchema struct {
	system ClientServerSystem
}

// NewClientOutboundOperationNameSchema creates a new naming schema for outbound operations from clients.
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

// NewServerInboundOperationNameSchema creates a new naming schema for inbound operations from servers.
func NewServerInboundOperationNameSchema(system ClientServerSystem) *Schema {
	return New(&serverInboundOperationNameSchema{system: system})
}

func (s *serverInboundOperationNameSchema) V0() string {
	return fmt.Sprintf("%s.request", s.system)
}

func (s *serverInboundOperationNameSchema) V1() string {
	return fmt.Sprintf("%s.server.request", s.system)
}
