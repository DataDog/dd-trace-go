package workflows

import "strings"

#Service: {
	image: string
	env?: {[string]: string | number | bool}
	options?: string
	ports?: [...string]
	volumes?: [...string]
}

#Image: {
	repository: string
	tag?:       string
}

#ToImage: {
	_svc: #Service
	_parts: [...string] & strings.Split(_svc.image, ":")

	repository: _parts[0]
	tag:        _parts[1]
} & #Image

_datadog_agent_svc: #Service & {
	"image": "datadog/agent:7.69.2"
	"env": {
		"DD_HOSTNAME":        "github-actions-worker"
		"DD_APM_ENABLED":     true
		"DD_BIND_HOST":       "0.0.0.0"
		"DD_API_KEY":         "invalid_key_but_this_is_fine"
		"DD_TEST_AGENT_HOST": "localhost"
		"DD_TEST_AGENT_PORT": 9126
	}
	"options": """
		--health-cmd "bash -c '</dev/tcp/127.0.0.1/8126'
		"""
	"ports": [
		"8125:8125/udp",
		"8126:8126",
	]
}

_testagent_svc: #Service & {
	"image": "ghcr.io/datadog/dd-apm-test-agent/ddapm-test-agent:v1.11.0"
	"env": {
		"LOG_LEVEL":                      "DEBUG"
		"TRACE_LANGUAGE":                 "golang"
		"ENABLED_CHECKS":                 "trace_stall,trace_count_header,trace_peer_service,trace_dd_service"
		"PORT":                           9126
		"DD_SUPPRESS_TRACE_PARSE_ERRORS": true
		"DD_POOL_TRACE_CHECK_FAILURES":   true
		"DD_DISABLE_ERROR_RESPONSES":     true
	}
	"ports": [
		"9126:9126",
	]
}

_cassandra_svc: #Service & {
	"image": "cassandra:3.11"
	"env": {
		"JVM_OPTS":                  "-Xms750m -Xmx750m"
		"CASSANDRA_CLUSTER_NAME":    "dd-trace-go-test-cluster"
		"CASSANDRA_DC":              "dd-trace-go-test-datacenter"
		"CASSANDRA_ENDPOINT_SNITCH": "GossipingPropertyFileSnitch"
	}
	"ports": [
		"9042:9042",
	]
}

_mysql_svc: #Service & {
	"image": "cimg/mysql:8.0"
	"env": {
		"MYSQL_ROOT_PASSWORD": "admin"
		"MYSQL_PASSWORD":      "test"
		"MYSQL_USER":          "test"
		"MYSQL_DATABASE":      "test"
	}
	"ports": [
		"3306:3306",
	]
}

_postgres_svc: #Service & {
	"image": "cimg/postgres:16.4"
	"env": {
		"POSTGRES_PASSWORD": "postgres"
		"POSTGRES_USER":     "postgres"
		"POSTGRES_DB":       "postgres"
	}
	"ports": [
		"5432:5432",
	]
}

_mssql_svc: #Service & {
	"image": "mcr.microsoft.com/mssql/server:2019-latest"
	"env": {
		"SA_PASSWORD": "myPassw0rd"
		"ACCEPT_EULA": "Y"
	}
	"ports": [
		"1433:1433",
	]
}

_consul_svc: #Service & {
	"image": "consul:1.6.0"
	"ports": [
		"8500:8500",
	]
}

_redis_svc: #Service & {
	"image": "redis:3.2"
	"ports": [
		"6379:6379",
	]
}

_valkey_svc: #Service & {
	"image": "valkey/valkey:8"
	"env": {
		"VALKEY_EXTRA_FLAGS": "--port 6380 --requirepass password-for-default"
	}
	"ports": [
		"6380:6380",
	]
}

_elasticsearch2_svc: #Service & {
	"image": "elasticsearch:2"
	"env": {
		// https://github.com/10up/wp-local-docker/issues/6
		"ES_JAVA_OPTS": "-Xms750m -Xmx750m"
	}
	"ports": [
		"9200:9200",
	]
}

_elasticsearch5_svc: #Service & {
	"image": "elasticsearch:5"
	"env": {
		// https://github.com/10up/wp-local-docker/issues/6
		"ES_JAVA_OPTS": "-Xms750m -Xmx750m"
	}
	"ports": [
		"9201:9200",
	]
}
_elasticsearch6_svc: #Service & {
	"image": "elasticsearch:6.8.13"
	"env": {
		// https://github.com/10up/wp-local-docker/issues/6
		"ES_JAVA_OPTS": "-Xms750m -Xmx750m"
	}
	"ports": [
		"9202:9200",
	]
}

_elasticsearch7_svc: #Service & {
	"image": "elasticsearch:7.14.1"
	"env": {
		// https://github.com/10up/wp-local-docker/issues/6
		"ES_JAVA_OPTS":   "-Xms750m -Xmx750m"
		"discovery.type": "single-node"
	}
	"ports": [
		"9203:9200",
	]
}

_elasticsearch8_svc: #Service & {
	"image": "elasticsearch:8.6.2"
	"env": {
		"ES_JAVA_OPTS":           "-Xms750m -Xmx750m"
		"discovery.type":         "single-node"
		"xpack.security.enabled": false
	}
	"ports": [
		"9204:9200",
	]
}

_mongo3_svc: #Service & {
	"image": "mongo:3"
	"ports": [
		"27018:27017",
	]
}

_mongo_svc: #Service & {
	"image": "mongo:8"
	"ports": [
		"27017:27017",
	]
}

_memcached_svc: #Service & {
	"image": "memcached:1.5.9"
	"ports": [
		"11211:11211",
	]
}

_kafka_svc: #Service & {
	"image": "confluentinc/confluent-local:7.5.0"
	"env": {
		"KAFKA_LISTENERS":                                "PLAINTEXT://0.0.0.0:9093,BROKER://0.0.0.0:9092,CONTROLLER://0.0.0.0:9094"
		"KAFKA_ADVERTISED_LISTENERS":                     "PLAINTEXT://localhost:9093,BROKER://localhost:9092"
		"KAFKA_REST_BOOTSTRAP_SERVERS":                   "PLAINTEXT://0.0.0.0:9093,BROKER://0.0.0.0:9092"
		"KAFKA_CONTROLLER_QUORUM_VOTERS":                 "1@localhost:9094"
		"KAFKA_LISTENER_SECURITY_PROTOCOL_MAP":           "BROKER:PLAINTEXT,PLAINTEXT:PLAINTEXT,CONTROLLER:PLAINTEXT"
		"KAFKA_INTER_BROKER_LISTENER_NAME":               "BROKER"
		"KAFKA_BROKER_ID":                                "1"
		"KAFKA_OFFSETS_TOPIC_REPLICATION_FACTOR":         "1"
		"KAFKA_OFFSETS_TOPIC_NUM_PARTITIONS":             "1"
		"KAFKA_TRANSACTION_STATE_LOG_REPLICATION_FACTOR": "1"
		"KAFKA_TRANSACTION_STATE_LOG_MIN_ISR":            "1"
		"KAFKA_GROUP_INITIAL_REBALANCE_DELAY_MS":         "0"
		"KAFKA_NODE_ID":                                  "1"
		"KAFKA_PROCESS_ROLES":                            "broker,controller"
		"KAFKA_CONTROLLER_LISTENER_NAMES":                "CONTROLLER"
	}
	"ports": [
		"9092:9092",
		"9093:9093",
	]
	"options":
		"--name \"kafka\""
}

_localstack_svc: #Service & {
	"image": "localstack/localstack:latest"
	"ports": [
		"4566:4566",
	]
}

_services: {
	"datadog-agent":  _datadog_agent_svc
	"testagent":      _testagent_svc
	"cassandra":      _cassandra_svc
	"mysql":          _mysql_svc
	"postgres":       _postgres_svc
	"mssql":          _mssql_svc
	"consul":         _consul_svc
	"redis":          _redis_svc
	"valkey":         _valkey_svc
	"elasticsearch2": _elasticsearch2_svc
	"elasticsearch5": _elasticsearch5_svc
	"elasticsearch6": _elasticsearch6_svc
	"elasticsearch7": _elasticsearch7_svc
	"elasticsearch8": _elasticsearch8_svc
	"mongo3":         _mongo3_svc
	"mongo":          _mongo_svc
	"memcached":      _memcached_svc
	"kafka":          _kafka_svc
	"localstack":     _localstack_svc
}
