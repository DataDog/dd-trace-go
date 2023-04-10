// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package ext

const (
	// DBApplication indicates the application using the database.
	DBApplication = "db.application"
	// DBName indicates the database name.
	DBName = "db.name"
	// DBType indicates the type of Database.
	DBType = "db.type"
	// DBInstance indicates the instance name of Database.
	DBInstance = "db.instance"
	// DBUser indicates the user name of Database, e.g. "readonly_user" or "reporting_user".
	DBUser = "db.user"
	// DBStatement records a database statement for the given database type.
	DBStatement = "db.statement"
)

// DBSystem indicates the database management system (DBMS) product being used.
// The following list includes the tag name and all the available values for it.
const (
	DBSystem                   = "db.system"
	DBSystemMemcached          = "memcached"
	DBSystemMySQL              = "mysql"
	DBSystemPostgreSQL         = "postgresql"
	DBSystemMicrosoftSQLServer = "mssql"
	// DBSystemOtherSQL is used for other SQL databases not listed above.
	DBSystemOtherSQL      = "other_sql"
	DBSystemElasticsearch = "elasticsearch"
	DBSystemRedis         = "redis"
	DBSystemMongoDB       = "mongodb"
	DBSystemCassandra     = "cassandra"
	DBSystemConsulKV      = "consul"
	DBSystemLevelDB       = "leveldb"
	DBSystemBuntDB        = "buntdb"
)

// MicrosoftSQLServer tags.
const (
	// MicrosoftSQLServerInstanceName indicates the Microsoft SQL Server instance name connecting to.
	MicrosoftSQLServerInstanceName = "db.mssql.instance_name"
)

// MongoDB tags.
const (
	// MongoDBCollection indicates the collection being accessed.
	MongoDBCollection = "db.mongodb.collection"
)

// Redis tags.
const (
	// RedisDatabaseIndex indicates the Redis database index connected to.
	RedisDatabaseIndex = "db.redis.database_index"
)
