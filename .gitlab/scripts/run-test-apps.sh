#!/usr/bin/env bash

echo "-> Installing agent"
DD_HOSTNAME=$(hostname) \
  DD_ENV="gitlab" \
  DD_API_KEY="$(aws ssm get-parameter --region us-east-1 --name ci.dd-trace-go.dd_api_key --with-decryption --query "Parameter.Value" --out text)" \
  DD_INSTALL_ONLY=true \
  DD_AGENT_MAJOR_VERSION=7 \
  DD_API_KEY=$DD_API_KEY \
  DD_SITE="datad0g.com" \
  bash -c "$(curl -L https://s3.amazonaws.com/dd-agent/scripts/install_script.sh)"

echo "-> Starting agent"
# see https://github.com/DataDog/datadog-agent/issues/14836
cp /etc/datadog-agent/security-agent.yaml.example /etc/datadog-agent/security-agent.yaml
service datadog-agent start

echo "-> Running test app"
cd ./profiler/internal/apps/unit-of-work && go test -v

echo "-> Stopping agent"
service datadog-agent stop
