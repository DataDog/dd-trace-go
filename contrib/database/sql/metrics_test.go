// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package sql

// Test that a sql.DBStat is collected and passed off to the pushFn every time pollDBStats is invoked
// func TestDBStats(t *testing.T){
// 	driverName := "postgres"
// 	assert.Equal(t, "dn", driverName)
// 	dsn := "postgres://postgres:postgres@127.0.0.1:5432/postgres?sslmode=disable"
// 	db, err := Open(driverName, dsn)
// 	require.NoError(t, err)
// }