#!/bin/bash

# Usage - run commands from the /integration_tests directory:
# To check if new changes to the library cause changes to any snapshots:
#   DD_API_KEY=XXXX aws-vault exec sandbox-account-admin -- ./run_integration_tests.sh
# To regenerate snapshots:
#   UPDATE_SNAPSHOTS=true DD_API_KEY=XXXX aws-vault exec sandbox-account-admin -- ./run_integration_tests.sh

set -e

# Disable deprecation warnings.
export SLS_DEPRECATION_DISABLE=*

# These values need to be in sync with serverless.yml, where there needs to be a function
# defined for every handler_runtime combination
LAMBDA_HANDLERS=("hello" "error")

LOGS_WAIT_SECONDS=20

integration_tests_dir=$(cd `dirname $0` && pwd)
echo $integration_tests_dir

script_utc_start_time=$(date -u +"%Y%m%dT%H%M%S")

mismatch_found=false

if [ -z "$AWS_SECRET_ACCESS_KEY" ]; then
    echo "No AWS credentials were found in the environment."
    echo "Note that only Datadog employees can run these integration tests."
    exit 1
fi

if [ -z "$DD_API_KEY" ]; then
    echo "No DD_API_KEY env var set, exiting"
    exit 1
fi

if [ -n "$UPDATE_SNAPSHOTS" ]; then
    echo "Overwriting snapshots in this execution"
fi

echo "Building Go binaries"
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o build/hello/bootstrap hello/main.go
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o build/error/bootstrap error/main.go
zip -j build/hello.zip build/hello/bootstrap
zip -j build/error.zip build/error/bootstrap

# Generate a random 8-character ID to avoid collisions with other runs
run_id=$(xxd -l 4 -c 4 -p < /dev/random)

# Always remove the stack before exiting, no matter what
function remove_stack() {
    echo "Removing functions"
    serverless remove --stage $run_id
}
trap remove_stack EXIT

sls --version

echo "Deploying functions"
sls deploy --stage $run_id

cd $integration_tests_dir

input_event_files=$(ls ./input_events)
# Sort event files by name so that snapshots stay consistent
input_event_files=($(for file_name in ${input_event_files[@]}; do echo $file_name; done | sort))

echo "Invoking functions"
set +e # Don't exit this script if an invocation fails or there's a diff
for input_event_file in "${input_event_files[@]}"; do
    for function_name in "${LAMBDA_HANDLERS[@]}"; do
        # Get event name without trailing ".json" so we can build the snapshot file name
        input_event_name=$(echo "$input_event_file" | sed "s/.json//")
        # Return value snapshot file format is snapshots/return_values/{handler}_{runtime}_{input-event}
        snapshot_path="$integration_tests_dir/snapshots/return_values/${function_name}_${input_event_name}.json"

        return_value=$(DD_API_KEY=$DD_API_KEY sls invoke --stage $run_id -f $function_name --path "$integration_tests_dir/input_events/$input_event_file")
        sls_invoke_exit_code=$?
        if [ $sls_invoke_exit_code -ne 0 ]; then
            return_value="Invocation failed"
        fi

        if [ ! -f $snapshot_path ]; then
            # If the snapshot file doesn't exist yet, we create it
            echo "Writing return value to $snapshot_path because no snapshot exists yet"
            echo "$return_value" >$snapshot_path
        elif [ -n "$UPDATE_SNAPSHOTS" ]; then
            # If $UPDATE_SNAPSHOTS is set to true, write the new logs over the current snapshot
            echo "Overwriting return value snapshot for $snapshot_path"
            echo "$return_value" >$snapshot_path
        else
            # Compare new return value to snapshot
            diff_output=$(echo "$return_value" | diff - $snapshot_path)
            if [ $? -eq 1 ]; then
                echo "Failed: Return value for $function_name does not match snapshot:"
                echo "$diff_output"
                mismatch_found=true
            else
                echo "Ok: Return value for $function_name with $input_event_name event matches snapshot"
            fi
        fi
    done
done
set -e

echo "Sleeping $LOGS_WAIT_SECONDS seconds to wait for logs to appear in CloudWatch..."
sleep $LOGS_WAIT_SECONDS

set +e # Don't exit this script if there is a diff or the logs endpoint fails
echo "Fetching logs for invocations and comparing to snapshots"
for function_name in "${LAMBDA_HANDLERS[@]}"; do
    function_snapshot_path="./snapshots/logs/$function_name.log"

    # Fetch logs with serverless cli, retrying to avoid AWS account-wide rate limit error
    retry_counter=0
    while [ $retry_counter -lt 10 ]; do
        raw_logs=$(serverless logs -f $function_name --stage $run_id --startTime $script_utc_start_time)
        fetch_logs_exit_code=$?
        if [ $fetch_logs_exit_code -eq 1 ]; then
            echo "Retrying fetch logs for $function_name..."
            retry_counter=$(($retry_counter + 1))
            sleep 10
            continue
        fi
        break
    done

    if [ $retry_counter -eq 9 ]; then
        echo "FAILURE: Could not retrieve logs for $function_name"
        echo "Error from final attempt to retrieve logs:"
        echo $raw_logs

        exit 1
    fi

    # Replace invocation-specific data like timestamps and IDs with XXXX to normalize logs across executions
    logs=$(
        echo "$raw_logs" |
            node parse-json.js |
            # Remove serverless cli errors
            sed '/Serverless: Recoverable error occurred/d' |
            # Remove dd-trace-go logs
            sed '/Datadog Tracer/d' |
            # Normalize Lambda runtime report logs
            perl -p -e 's/(RequestId|TraceId|init|SegmentId|Duration|Memory Used|"e"):( )?[a-z0-9\.\-]+/\1:\2XXXX/g' |
            # Normalize DD APM headers and AWS account ID
            perl -p -e "s/(Current span ID:|Current trace ID:|account_id:) ?[0-9]+/\1XXXX/g" |
            # Strip API key from logged requests
            perl -p -e "s/(api_key=|'api_key': ')[a-z0-9\.\-]+/\1XXXX/g" |
            # Normalize ISO combined date-time
            perl -p -e "s/[0-9]{4}\-[0-9]{2}\-[0-9]{2}(T?)[0-9]{2}:[0-9]{2}:[0-9]{2}(\.[0-9]+ \(\-?[0-9:]+\))?Z/XXXX-XX-XXTXX:XX:XX.XXXZ/" |
            # Normalize log timestamps
            perl -p -e "s/[0-9]{4}(\-|\/)[0-9]{2}(\-|\/)[0-9]{2} [0-9]{2}:[0-9]{2}:[0-9]{2}(\.[0-9]+( \(\-?[0-9:]+\))?)?/XXXX-XX-XX XX:XX:XX.XXX/" |
            # Normalize DD trace ID injection
            perl -p -e "s/(dd\.trace_id=)[0-9]+ (dd\.span_id=)[0-9]+/\1XXXX \2XXXX/" |
            # Normalize execution ID in logs prefix
            perl -p -e $'s/[0-9a-z]+\-[0-9a-z]+\-[0-9a-z]+\-[0-9a-z]+\-[0-9a-z]+\t/XXXX-XXXX-XXXX-XXXX-XXXX\t/' |
            # Normalize layer version tag
            perl -p -e "s/(dd_lambda_layer:datadog-go)[0-9]+\.[0-9]+\.[0-9]+/\1X\.X\.X/g" |
            # Normalize package version tag
            perl -p -e "s/(datadog_lambda:v)[0-9]+\.[0-9]+\.[0-9]+/\1X\.X\.X/g" |
            # Normalize golang version tag
            perl -p -e "s/(go)[0-9]+\.[0-9]+\.[0-9]+/\1X\.X\.X/g" |
            # Normalize data in logged traces
            perl -p -e 's/"(span_id|apiid|runtime-id|record_ids|parent_id|trace_id|start|duration|tcp\.local\.address|tcp\.local\.port|dns\.address|request_id|function_arn|x-datadog-trace-id|x-datadog-parent-id|datadog_lambda|dd_trace)":\ ("?)[a-zA-Z0-9\.:\-]+("?)/"\1":\2XXXX\3/g' |
            # Remove metrics and metas in logged traces (their order is inconsistent)
            perl -p -e 's/"(meta|metrics)":{(.*?)}/"\1":{"XXXX": "XXXX"}/g' |
            # Strip out run ID (from function name, resource, etc.)
            perl -p -e "s/$run_id/XXXX/g" |
            # Normalize data in logged metrics
            perl -p -e 's/"(points\\\":\[\[)([0-9]+)/\1XXXX/g' |
            # Remove INIT_START log
            perl -p -e "s/INIT_START.*//g"
    )

    if [ ! -f $function_snapshot_path ]; then
        # If no snapshot file exists yet, we create one
        echo "Writing logs to $function_snapshot_path because no snapshot exists yet"
        echo "$logs" >$function_snapshot_path
    elif [ -n "$UPDATE_SNAPSHOTS" ]; then
        # If $UPDATE_SNAPSHOTS is set to true write the new logs over the current snapshot
        echo "Overwriting log snapshot for $function_snapshot_path"
        echo "$logs" >$function_snapshot_path
    else
        # Compare new logs to snapshots
        diff_output=$(echo "$logs" | diff - $function_snapshot_path)
        if [ $? -eq 1 ]; then
            echo "Failed: Mismatch found between new $function_name logs (first) and snapshot (second):"
            echo "$diff_output"
            mismatch_found=true
        else
            echo "Ok: New logs for $function_name match snapshot"
        fi
    fi
done
set -e

if [ "$mismatch_found" = true ]; then
    echo "FAILURE: A mismatch between new data and a snapshot was found and printed above."
    echo "If the change is expected, generate new snapshots by running 'UPDATE_SNAPSHOTS=true DD_API_KEY=XXXX ./run_integration_tests.sh'"
    exit 1
fi

if [ -n "$UPDATE_SNAPSHOTS" ]; then
    echo "SUCCESS: Wrote new snapshots for all functions"
    exit 0
fi

echo "SUCCESS: No difference found between snapshots and new return values or logs"
