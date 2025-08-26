"name": "Unit and Integration Tests"

"on": "workflow_call": "inputs": "go-version": {
	"required": true
	"type":     "string"
}

"env": {
	// Increase time WAF time budget to reduce CI flakiness
	// Users may build our library with GOTOOLCHAIN=local. If they do, and our
	// go.mod file specifies a newer Go version than their local toolchain, their
	// build will break. Run our tests with GOTOOLCHAIN=local to ensure that
	// our library builds with all of the Go versions we claim to support,
	// without having to download a newer one.
	"DD_APPSEC_WAF_TIMEOUT": "1m"
	"GOTOOLCHAIN":           "local"
	"GODEBUG":               "x509negativeserial=1"
	"GOEXPERIMENT":          "synctest" // TODO: remove once go1.25 is the minimum supported version
	"TEST_RESULT_PATH":      "/tmp/test-results"
}

"permissions": "contents": "read"

"jobs": {
	"set-up": {
		"runs-on": "ubuntu-latest"
		"outputs": {
			"matrix": "${{ steps.matrix.outputs.matrix }}"
		}
		"steps": [
			{
				"name": "Restore repo cache"
				"uses": "actions/cache@0400d5f644dc74513175e3cd8d07132dd4860809"
				"with": {
					"path": ".git"
					"key":  "gitdb-${{ github.repository_id }}-${{ github.sha }}"
				}
			},
			{
				"name": "Checkout"
				"uses": "actions/checkout@11bd71901bbe5b1630ceea73d27597364c9af683"
				"with": {
					"ref":   "${{ github.sha }}"
					"clean": false
				}
			},
			{
				"name": "Compute Matrix"
				"id":   "matrix"
				"run": """
					    echo -n "matrix="                      >> "${GITHUB_OUTPUT}"
					    go run ./scripts/ci_contrib_matrix.go  >> "${GITHUB_OUTPUT}"
					"""
			},
		]
	}
	"test-contrib-matrix": {
		"needs": [
			"set-up",
		]
		"runs-on": "group": "APM Larger Runners"
		"env": {
			"INTEGRATION": true
		}
		"strategy": {
			"matrix": {
				"chunk": "${{ fromJson(needs.set-up.outputs.matrix) }}"
			}
		}
		"services": _services
		"steps": [
			{
				"name": "Restore repo cache"
				"uses": "actions/cache@0400d5f644dc74513175e3cd8d07132dd4860809"
				"with": {
					"path": ".git"
					"key":  "gitdb-${{ github.repository_id }}-${{ github.sha }}"
				}
			},
			{
				"name": "Checkout"
				"uses": "actions/checkout@11bd71901bbe5b1630ceea73d27597364c9af683"
				"with": {
					"ref":   "${{ github.sha }}"
					"clean": false
				}
			},
			{
				"name": "Setup Go and development tools"
				"uses": "./.github/actions/setup-go"
				"with": {
					"go-version": "${{ inputs.go-version }}"
					"tools-dir":  "${{ github.workspace }}/_tools"
					"tools-bin":  "${{ github.workspace }}/bin"
				}
			},
			{
				"name": "Test Contrib"
				"if":   "always()"
				"env": {
					"TEST_RESULTS": "${{ env.TEST_RESULT_PATH }}"
				}
				"run": """
					    export PATH="${{ github.workspace }}/bin:${PATH}"
					    ./scripts/ci_test_contrib.sh default ${{ toJson(matrix.chunk) }}
					"""
			},
			{
				"name":              "Upload the results to Datadog CI App"
				"if":                "always()"
				"continue-on-error": true
				"uses":              "./.github/actions/dd-ci-upload"
				"with": {
					"dd-api-key": "${{ secrets.DD_CI_API_KEY }}"
					"path":       "${{ env.TEST_RESULT_PATH }}"
					"tags":       "go:${{ inputs.go-version }},arch:${{ runner.arch }},os:${{ runner.os }},distribution:${{ runner.distribution }}"
				}
			},
			{
				"name":              "Upload Coverage"
				"if":                "always()"
				"continue-on-error": true
				"shell":             "bash"
				"run":               "bash <(curl -s https://codecov.io/bash) -t ${{ secrets.CODECOV_TOKEN }}"
			},
		]
	}
	"test-contrib": {
		"needs": [
			"test-contrib-matrix",
		]
		"runs-on": "group": "APM Larger Runners"
		"if":                "success() || failure()"
		"continue-on-error": true
		"steps": [
			{
				"name": "Success"
				"if":   "needs.test-contrib-matrix.result == 'success'"
				"run":  "echo 'Success!'"
			},
			{
				"name": "Failure"
				"if":   "needs.test-contrib-matrix.result != 'success'"
				"run":  "echo 'Failure!' && exit 1"
			},
		]
	}
	"test-core": {
		"runs-on": "group": "APM Larger Runners"
		"env": {
			"INTEGRATION": true
		}
		"steps": [
			{
				"name": "Restore repo cache"
				"uses": "actions/cache@0400d5f644dc74513175e3cd8d07132dd4860809"
				"with": {
					"path": ".git"
					"key":  "gitdb-${{ github.repository_id }}-${{ github.sha }}"
				}
			},
			{
				"name": "Checkout"
				"uses": "actions/checkout@11bd71901bbe5b1630ceea73d27597364c9af683"
				"with": {
					"ref":   "${{ github.sha }}"
					"clean": false
				}
			},
			{
				"name": "Setup Go and development tools"
				"uses": "./.github/actions/setup-go"
				"with": {
					"go-version": "${{ inputs.go-version }}"
					"tools-dir":  "${{ github.workspace }}/_tools"
					"tools-bin":  "${{ github.workspace }}/bin"
				}
			},
			{
				"name": "Start datadog/agent"
				"uses": "./.github/actions/run-service"
				"with": {
					#ToImage & {_svc: _datadog_agent_svc}
				}
			},
			{
				"name": "Test Core"
				"env": {
					"DD_APPSEC_WAF_TIMEOUT": "1h"
					"TEST_RESULTS":          "${{ env.TEST_RESULT_PATH }}"
				}
				"run": """
					    export PATH="${{ github.workspace }}/bin:${PATH}"
					    ls -al "${{ github.workspace }}/bin"
					    ./scripts/ci_test_core.sh
					"""
			},
			{
				"name":              "Upload the results to Datadog CI App"
				"if":                "always()"
				"continue-on-error": true
				"uses":              "./.github/actions/dd-ci-upload"
				"with": {
					"dd-api-key": "${{ secrets.DD_CI_API_KEY }}"
					"path":       "${{ env.TEST_RESULT_PATH }}"
					"tags":       "go:${{ inputs.go-version }},arch:${{ runner.arch }},os:${{ runner.os }},distribution:${{ runner.distribution }}"
				}
			},
			{
				"name":              "Upload Coverage"
				"if":                "always()"
				"continue-on-error": true
				"shell":             "bash"
				"run":               "bash <(curl -s https://codecov.io/bash) -t ${{ secrets.CODECOV_TOKEN }}"
			},
		]
	}
	"upload-test-results": {
		"needs": [
			"test-contrib",
			"test-core",
		]
		"if": "always()"
		"runs-on": "group": "APM Larger Runners"
		"services": {
			"datadog-agent": _datadog_agent_svc
			"testagent":     _testagent_svc
		}
		"steps": [
			{
				"name":  "Get Datadog APM Test Agent Logs"
				"if":    "always()"
				"shell": "bash"
				"run":   "docker logs ${{ job.services.testagent.id }}"
			},
			{
				"name":  "Get Datadog APM Test Agent Trace Check Summary Results"
				"if":    "always()"
				"shell": "bash"
				"run": """
					    RESPONSE=$(curl -s -w "\\n%{http_code}" -o response.txt "http://127.0.0.1:9126/test/trace_check/failures?return_all=true")
					    RESPONSE_CODE=$(echo "$RESPONSE" | awk 'END {print $NF}')
					    SUMMARY_RESPONSE=$(curl -s -w "\\n%{http_code}" -o summary_response.txt "http://127.0.0.1:9126/test/trace_check/summary?return_all=true")
					    SUMMARY_RESPONSE_CODE=$(echo "$SUMMARY_RESPONSE" | awk 'END {print $NF}')
					    if [[ $RESPONSE_CODE -eq 200 ]]; then
					        echo " "
					        cat response.txt
					        echo " - All APM Test Agent Check Traces returned successful!"
					        echo "APM Test Agent Check Traces Summary Results:"
					        cat summary_response.txt | jq "."
					    else
					        echo "APM Test Agent Check Traces failed with response code: $RESPONSE_CODE"
					        echo "Failures:"
					        cat response.txt
					        echo "APM Test Agent Check Traces Summary Results:"
					        cat summary_response.txt | jq "."
					        exit 1
					    fi
					"""
			},
		]
	}
}
