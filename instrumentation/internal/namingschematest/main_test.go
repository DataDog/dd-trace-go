// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package namingschematest

import (
	"testing"

	"github.com/DataDog/dd-trace-go/instrumentation/internal/namingschematest/v2/harness"

	"github.com/DataDog/dd-trace-go/instrumentation/testutils/containers/v2"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/env"
)

var kafkaAddr string

func TestNamingSchema(t *testing.T) {
	if _, ok := env.LookupEnv("INTEGRATION"); !ok {
		t.Skip("🚧 Skipping integration test (INTEGRATION environment variable is not set)")
	}
	t.Setenv("__DD_TRACE_SQL_TEST", "true")

	_, kafkaAddr = containers.StartKafkaTestContainer(
		t,
		[]string{segmentioTopic, confluentKafkaV2Topic, saramaTopic},
	)

	testCases := []harness.TestCase{
		gqlgen,
		awsSDKV1,
		awsSDKV1Messaging,
		awsSDKV2,
		awsSDKV2Messaging,
		// confluentKafkaV1, // this one lives in a separate package due to build errors
		confluentKafkaV2,
		databaseSQL_SQLServer,
		databaseSQL_Postgres,
		databaseSQL_PostgresWithRegisterOverride,
		databaseSQL_MySQL,
		httpTreeMuxTestCase,
		elasticV6,
		goRestfulV3,
		ginTest,
		globalsignMgo,
		mongoDriverTest,
		chiV1Test,
		chiV5Test,
		goPGv10Test,
		goRedisV1Test,
		goRedisV7Test,
		goRedisV8Test,
		gocqlTest,
		grpcServerTest,
		grpcClientTest,
		fiberV2Test,
		redigoTest,
		netHTTPServerServeMux,
		netHTTPServerWrapHandler,
		netHTTPClient,
		gomemcache,
		gcpPubsub,
		urfaveNegroni,
		twitchTVTwirp,
		tidwallBuntDB,
		syndtrGoLevelDB,
		shopifySarama,
		segmentioKafkaGo,
		redisGoRedisV9,
		olivereElasticV5,
		labstackEchoV4,
		julienschmidtHTTPRouter,
		ibmSarama,
		hashicorpConsul,
		hashicorpVault,
		graphGophersGraphQLGo,
		graphqlGo,
		gorillaMux,
	}
	for _, tc := range testCases {
		harness.RunTest(t, tc)
	}
}
