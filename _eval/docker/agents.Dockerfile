ARG GO_VERSION=1.25.0
FROM golang:${GO_VERSION}-bookworm AS go

FROM node:22-bookworm AS base

COPY --from=go /usr/local/go /usr/local/go

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        bash \
        build-essential \
        ca-certificates \
        curl \
        git \
        jq \
        make \
        patch \
        python3 \
        ripgrep \
        unzip \
        util-linux \
    && rm -rf /var/lib/apt/lists/*

COPY entrypoint.sh /usr/local/bin/agent-eval-entrypoint
RUN chmod 0755 /usr/local/bin/agent-eval-entrypoint

ENV PATH="/usr/local/go/bin:/go/bin:${PATH}"
ENTRYPOINT ["/usr/local/bin/agent-eval-entrypoint"]

FROM base AS claude
ARG CLAUDE_CLI_VERSION=2.1.231
ENV AGENT_EVAL_AGENT=claude
RUN npm install --global "@anthropic-ai/claude-code@${CLAUDE_CLI_VERSION}" \
    && claude --version

FROM base AS codex
ARG CODEX_CLI_VERSION=0.147.0
ENV AGENT_EVAL_AGENT=codex
RUN npm install --global "@openai/codex@${CODEX_CLI_VERSION}" \
    && codex --version
