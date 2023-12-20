#!/usr/bin/env bash
set -e

contrib=""
sleeptime=10
unset INTEGRATION
unset DD_APPSEC_ENABLED

if [[ $# -eq 0 ]]; then
	echo "Use the -h flag for help"
fi

if [[ "$(uname -s)" = 'Darwin' && "$(uname -m)" = 'arm64' ]]; then
  # Needed to run integration tests on Apple Silicon
  export DOCKER_DEFAULT_PLATFORM=linux/amd64
fi

while [[ $# -gt 0 ]]; do
	case $1 in
		-a|--appsec)
			export DD_APPSEC_ENABLED=true
			shift
			;;
		-i|--integration)
			export INTEGRATION=true
			shift
			;;
		-c|--contrib)
			contrib=true
			shift
			;;
		--all)
			contrib=true
			lint=true
			export DD_APPSEC_ENABLED=true
			export DD_TEST_APPS_ENABLED=true
			export INTEGRATION=true
			shift
			;;
		-s|--sleep)
			sleeptime=$2
			shift
			shift
			;;
		-l|--lint)
			lint=true
			shift
			;;
		-t|--tools)
			tools=true
			shift
			;;
		-h|--help)
			echo "test.sh - Run the tests for dd-trace-go"
			echo "	this script requires gotestsum, goimports, docker and docker-compose."
			echo "	-l | --lint		- Run the linter"
			echo "	-a | --appsec		- Test with appsec enabled"
			echo "	-i | --integration	- Run integration tests. This requires docker and docker-compose. Resource usage is significant when combined with --contrib"
			echo "	-c | --contrib		- Run contrib tests"
			echo "	--all			- Synonym for -l -a -i -c"
			echo "	-s | --sleep		- The amount of seconds to wait for docker containers to be ready - default: 30 seconds"
			echo "	-t | --tools		- Install gotestsum and goimports"
			echo "	-h | --help		- Print this help message"
			exit 0
			;;
		*)
			echo "Ignoring unknown argument $1"
			shift
			;;
	esac
done

if [[ ! -z "$tools" ]]; then
    pushd /tmp
    go install golang.org/x/tools/cmd/goimports@latest
    go install gotest.tools/gotestsum@latest
    popd
fi

if [[ ! -z "$lint" ]]; then
    echo "Running Linter"
    goimports -e -l -local github.com/DataDog/dd-trace-go/v2 .
fi

if [[ "$INTEGRATION" != "" ]]; then
	## Make sure we shut down the docker containers on exit.
	function finish {
		echo Cleaning up...
		docker-compose down
	}
	trap finish EXIT
	if [[ "$contrib" != "" ]]; then
		## Start these now so they'll be ready by the time we run integration tests.
		docker-compose up -d
	else
		## If we're not testing contrib, we only need the trace agent.
		docker-compose up -d datadog-agent
	fi
fi

## CORE
echo testing core
pkg_names=$(go list ./...)
nice -n20 gotestsum --junitfile ./gotestsum-report.xml -- -race -v -coverprofile=core_coverage.txt -covermode=atomic $pkg_names && true

if [[ "$contrib" != "" ]]; then
	## CONTRIB
	echo testing contrib

	if [[ "$INTEGRATION" != "" ]]; then
		## wait for all the docker containers to be "ready"
		echo Waiting for docker for $sleeptime seconds
		sleep $sleeptime
	fi

  find . -mindepth 2 -type f -name go.mod | while read -r go_mod_path; do
    dir=$(dirname "$go_mod_path")
    cd "$dir"
    echo testing "$dir"
    pkgs=$(go list ./... | grep -v -e google.golang.org/api | tr '\n' ' ' | sed 's/ $//g')
    pkg_id=$(echo "$pkgs" | head -n1 | sed 's/\//_/g')
    if [[ -z "$pkg_id" ]]; then
      cd - > /dev/null
      continue
    fi
    nice -n20 gotestsum --junitfile "./gotestsum-report.$pkg_id.xml" -- -race -v -coverprofile="contrib_coverage.$pkg_id.txt" -covermode=atomic $pkgs
    cd - > /dev/null
  done
fi
