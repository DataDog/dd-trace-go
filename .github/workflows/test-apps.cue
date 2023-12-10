// IMPORTANT: Keep this is sync with scenario_test.go.
//
// Note on cost: We currently run each scenario every night for ~10min against
// staging and prod using the default runners with 2 CPU cores. This means we
// pay ~$5/scenario/month [1]. In the future the config below could be extended
// to allow different runners and durations for different scenarios. It might
// also make sense to add a weekly run with a longer duration.
//
// [1] https://docs.github.com/en/billing/managing-billing-for-github-actions/about-billing-for-github-actions#per-minute-rates
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
        name: "memory-leak/stack"
    },
]

#args: {
    rps: {
        env: "DD_TEST_APPS_REQUESTS_PER_SECOND",
        type: "number",
        description: "Requests per second",
        default: 5,
        pr_default: default,
    },
    scenario_duration: {
        env: "DD_TEST_APPS_TOTAL_DURATION",
        type: "number",
        description: "Scenario duration (s)",
        default: "\(10*60)s",
        pr_default: "60s",
    },
    profile_period: {
        env: "DD_TEST_APPS_PROFILE_PERIOD",
        type: "number",
        description: "Profile period (s)",
        default: "60s",
        pr_default: "10s",
    },
}

#envs: [
    {
        name: "prod",
        site: "datadoghq.com",
        key: "DD_TEST_APP_API_KEY",
    },
    {
        name: "staging",
        site: "datad0g.com",
        key: "DD_TEST_AND_DEMO_API_KEY",
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

        for scenario in #scenarios {
            "scenario: \(scenario.name)": {
                type: "boolean",
                default: true | false,
            }
        }

    }
}

name: "Test Apps"
on: {
    pull_request: {}

    // used by nightly cron schedule triggers
    workflow_call: #inputs & {
        inputs: {
            [=~ "scenario:"]: {default: true},
            ref: {
                description: "The branch to run the workflow on",
                required: true,
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
  DD_TAGS: "github_run_id:${{ github.run_id }} github_run_number:${{ github.run_number }}",
}

#if_not_fork: "(github.event_name != 'pull_request' || github.event.pull_request.head.repo.full_name == 'DataDog/dd-trace-go')"

jobs: {
    for i, scenario in #scenarios {
        for j, env in #envs {
            "job-\(i)-\(j)": {
                name: "\(scenario.name) (\(env.name))",
                "runs-on": "ubuntu-latest",

                #if_scenario: "inputs['scenario: \(scenario.name)']",
                #if_env: "inputs['env: \(env.name)']",
                
                if: "\(#if_scenario) && \(#if_env) && \(#if_not_fork)"
                steps: [
                    {
                        name: "Checkout Code",
                        uses: "actions/checkout@v3",
                        with: {ref: "${{ inputs.ref || github.ref }}"},
                    },
                    {
                        name: "Start Agent",
                        uses: "datadog/agent-github-action@v1.3",
                        with: {
                            api_key: "${{ secrets['\(env.key)'] }}",
                            datadog_site: "\(env.site)",
                        },
                    },
                    {
                        name: "Setup Go"
                        uses: "actions/setup-go@v3",
                        with: {
                            "go-version": "stable",
                            "check-latest": true,
                            cache: true,
                        },
                    },
                    {
                        name: "Run Scenario"
                        env: {
                            for name, arg in #args {
                                "\(arg.env)": "${{ inputs['arg: \(name)'] || '\(arg.pr_default)' }}",
                            }
                        },
                        run: "cd ./internal/apps && ./run-scenario.bash '\(scenario.name)'"
                    },
                ]
            }
        },
    },
}