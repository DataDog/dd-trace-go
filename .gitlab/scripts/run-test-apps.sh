#!/usr/bin/env bash
set -x

export DD_API_KEY="$(aws ssm get-parameter --region us-east-1 --name ci.dd-trace-go.dd_api_key --with-decryption --query "Parameter.Value" --out text)"

DD_INSTALL_ONLY=true DD_AGENT_MAJOR_VERSION=7 DD_API_KEY=$DD_API_KEY DD_SITE="datad0g.com" bash -c "$(curl -L https://s3.amazonaws.com/dd-agent/scripts/install_script.sh)"

# see https://github.com/DataDog/datadog-agent/issues/14836
cp /etc/datadog-agent/security-agent.yaml.example /etc/datadog-agent/security-agent.yaml
service datadog-agent start

tail -n500 /var/log/datadog/*
service datadog-agent status

cd ./profiler/internal/apps/unit-of-work && TestUnitOfWork=true go test -v

service datadog-agent stop
