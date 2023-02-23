#!/usr/bin/env bash
set -x

export DD_API_KEY="$(aws ssm get-parameter --region us-east-1 --name ci.dd-trace-go.dd_api_key --with-decryption --query "Parameter.Value" --out text)"

DD_AGENT_MAJOR_VERSION=7 DD_API_KEY=$DD_API_KEY DD_SITE="datad0g.com" bash -c "$(curl -L https://s3.amazonaws.com/dd-agent/scripts/install_script.sh)"

cd ./profiler/internal/apps/unit-of-work && TestUnitOfWork=true go test -v

service datadog-agent stop
