package workflows

"name": "Pull Request Tests"

"on": "pull_request": "types": [
	"opened",
	"synchronize",
	"reopened",
]

"concurrency": {
	"group":              "${{ github.ref }}"
	"cancel-in-progress": true
}

"jobs": {
	"warm-repo-cache": {
		"runs-on": "ubuntu-latest"
		"steps": [
			{
				"name": "Checkout"
				"uses": "actions/checkout@11bd71901bbe5b1630ceea73d27597364c9af683"
				"with": {
					"ref": "${{ github.event.pull_request.head.sha }}"
				}
			},
			{
				"name": "Cache"
				"uses": "actions/cache@0400d5f644dc74513175e3cd8d07132dd4860809"
				"with": {
					"path": ".git"
					"key":  "gitdb-${{ github.repository_id }}-${{ github.sha }}"
				}
			},
		]
	}
	"warm-services-cache": {
		"runs-on": "ubuntu-latest"
		"strategy": {
			"matrix": {
				"image": [
					#ToImage & {_svc: _datadog_agent_svc},
				]
			}
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
				"name": "Warm up service"
				"uses": "./.github/actions/warm-up-service"
				"with": {
					"repository": "${{ matrix.image.repository }}"
					"tag":        "${{ matrix.image.tag }}"
				}
			},
		]
	}
	"unit-integration-tests": {
		"name": "PR Unit and Integration Tests"
		"needs": [
			"warm-repo-cache",
			"warm-services-cache",
		]
		"strategy": {
			"matrix": {
				"go-version": _go_versions
			}
			"fail-fast": false
		}
		"uses": "./.github/workflows/unit-integration-tests.yml"
		"with": {
			"go-version": "${{ matrix.go-version }}"
		}
		"secrets": "inherit"
	}
	"multios-unit-tests": {
		"needs": [
			"warm-repo-cache",
			"warm-services-cache",
		]
		"strategy": {
			"matrix": {
				"runs-on": ["macos-latest", "windows-latest", "ubuntu-latest"]
				"go-version": _go_versions
			}
			"fail-fast": false
		}
		"uses": "./.github/workflows/multios-unit-tests.yml"
		"with": {
			"go-version": "${{ matrix.go-version }}"
			"runs-on":    "${{ matrix.runs-on }}"
		}
		"secrets": "inherit"
	}
	// This is a simple join point to make it easy to set up branch protection rules in GitHub.
	"pull-request-tests-done": {
		"name": "PR Unit and Integration Tests / ${{ matrix.name }}"
		"strategy": {
			"matrix": {
				"name": ["test-contrib", "test-core"]
			}
		}
		"needs": [
			"unit-integration-tests",
			"multios-unit-tests",
		]
		"runs-on": "ubuntu-latest"
		"if":      "success() || failure()"
		"steps": [
			{
				"name": "Success"
				"if":   "needs.unit-integration-tests.result == 'success' && needs.multios-unit-tests.result == 'success'"
				"run":  "echo 'Success!'"
			},
			{
				"name": "Failure"
				"if":   "needs.unit-integration-tests.result != 'success' || needs.multios-unit-tests.result != 'success'"
				"run":  "echo 'Failure!' && exit 1"
			},
		]
	}
}
