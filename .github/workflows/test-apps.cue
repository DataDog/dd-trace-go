// See /internal/apps/README.md for more information.
import "encoding/json"

#scenarios: [
    {
        name: "unit-of-work/v1",
    },
    {
        name: "unit-of-work/v2",
    },
    {
        name: "memory-leak/goroutine",
    },
    {
        name: "memory-leak/heap",
    },
    {
        name: "memory-leak/goroutine-heap"
    },
]

#args: {
    rps: {
        env: "DD_TEST_APPS_REQUESTS_PER_SECOND",
        type: "number",
        description: "Requests per second",
        default: 5,
    },
    scenario_duration: {
        env: "DD_TEST_APPS_TOTAL_DURATION",
        type: "string",
        description: "Scenario duration",
        default: "10m",
    },
    profile_period: {
        env: "DD_TEST_APPS_PROFILE_PERIOD",
        type: "string",
        description: "Profile period",
        default: "60s",
    },
    tags: {
        env: false,
        type: "string",
        description: "Extra DD_TAGS",
        default: "trigger:manual",
    },
}

#envs: [
    {
        name: "prod",
        site: "datadoghq.com",
    },
    {
        name: "staging",
        site: "datad0g.com",
    },
]

#inputs: {
    inputs: {
        [string]: _,

        for env in #envs {
            "env: \(env.name)": {
                type: "boolean",
                default: true,
            },
        }

        for name, arg in #args {
            "arg: \(name)": {
                type: arg.type,
                default: arg.default,
                description: arg.description,
            },
        }

        scenarios: {
            type: "string",
            default: json.Marshal([
                for scenario in #scenarios {
                    scenario.name
                }
            ]),
            description: "Scenarios to run"
        },
    }
}

name: "Test Apps"
on: {
    // used by nightly cron schedule triggers
    workflow_call: #inputs & {
        inputs: {
            [=~ "scenario:"]: {default: true},
            ref: {
                description: "The branch to run the workflow on",
                required: false,
                type: "string",
            },
        } 
    },

    // used for manual triggering
    workflow_dispatch: #inputs & {
        inputs: {[=~ "scenario:"]: {default: false}}
    }
}

env: {
  DD_ENV: "github",
  DD_TAGS: "github_run_id:${{ github.run_id }} github_run_number:${{ github.run_number }} ${{ inputs['arg: tags'] }}",
}

permissions: {
    contents: "read",
    "id-token": "write",
}

jobs: {
    for i, scenario in #scenarios {
        for j, env in #envs {
            "job-\(i)-\(j)": {
                name: "\(scenario.name) (\(env.name))",
                "runs-on": "ubuntu-latest",

                #if_scenario: "contains(fromJSON(inputs['scenarios']), '\(scenario.name)')",
                #if_env: "inputs['env: \(env.name)']",
                
                if: "\(#if_scenario) && \(#if_env)"
                steps: [
                    {
                        name: "Checkout Code",
                        uses: "actions/checkout@9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0", // v7.0.0
                        with: {
                        "persist-credentials": false,
                        ref:                   "${{ inputs.ref || github.ref }}",
                    },
                    },
                    {
                        name: "Get Datadog credentials",
                        id: "dd-sts",
                        uses: "DataDog/dd-sts-action@2e8187910199bd93129520183c093e19aa585c75",
                        with: {
                            policy: "dd-trace-go",
                        },
                    },
                    {
                        name: "Start Agent",
                        uses: "datadog/agent-github-action@8240b406d73cb84cd5085a3919a78f59c258da3a", // v1.3.1
                        with: {
                            api_key: "${{ steps.dd-sts.outputs.api_key }}",
                            datadog_site: "\(env.site)",
                        },
                    },
                    {
                        name: "Setup Go"
                        uses: "actions/setup-go@4a3601121dd01d1626a1e23e37211e3254c1c06c", // v6.4.0
                        with: {
                            "go-version": "stable",
                            "check-latest": true,
                            cache: true,
                        },
                    },
                    {
                        name: "Run Scenario"
                        env: {
                            // args.env is (string|false), so we use null coalescing to type cast to string
                            for name, arg in #args if (*(arg.env&string) | "") != "" {
                                "\(arg.env)": "${{ inputs['arg: \(name)'] }}",
                            }
                        },
                        run: "cd ./internal/apps && ./run-scenario.bash '\(scenario.name)'"
                    },
                ]
            }
        },
    },
}