#!/usr/bin/env bash
set -eu

stop_agent() {
  echo "-> Stopping agent"
  service datadog-agent stop
}
trap "stop_agent" ERR

echo "-> Installing agent"
DD_HOSTNAME=$(hostname) \
	DD_SITE="datad0g.com" \
  DD_API_KEY="$(aws ssm get-parameter --region us-east-1 --name ci.dd-trace-go.dd_api_key --with-decryption --query "Parameter.Value" --out text)" \
  DD_INSTALL_ONLY=true \
  DD_AGENT_MAJOR_VERSION=7 \
  bash -c "$(curl -L https://s3.amazonaws.com/dd-agent/scripts/install_script.sh)"

echo "-> Starting agent"
# see https://github.com/DataDog/datadog-agent/issues/14836
cp /etc/datadog-agent/security-agent.yaml.example /etc/datadog-agent/security-agent.yaml
service datadog-agent start

echo "-> Running unit-of-work test app"
cd ./internal/apps/unit-of-work && ./run.bash

stop_agent
