// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package namingschematest

import (
	"os"
	"testing"

	"github.com/DataDog/dd-trace-go/instrumentation/internal/namingschematest/harness"
)

func TestNamingSchema(t *testing.T) {
	if _, ok := os.LookupEnv("INTEGRATION"); !ok {
		t.Skip("🚧 Skipping integration test (INTEGRATION environment variable is not set)")
	}
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
		ginGonicGin,
		globalsignMgo,
		goMongodbOrgMongoDriver,
		netHTTPServer,
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
	}
	for _, tc := range testCases {
		harness.RunTest(t, tc)
	}
}
