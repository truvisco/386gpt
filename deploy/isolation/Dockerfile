FROM golang:1.26.0-bookworm@sha256:2a0ba12e116687098780d3ce700f9ce3cb340783779646aafbabed748fa6677c AS go
FROM node:24-bookworm-slim@sha256:0e0ff40c39bc087845bfb27465a0df4ea419520094bc35842ff83dd8cbe6f9b6 AS node
FROM python:3.11-slim-bookworm@sha256:a36c24f9cbdf4fd0f52d67f0823eeac19c2028c637cecc392d97f980d4fec56b
ENV PYTHONDONTWRITEBYTECODE=1 PYTHONUNBUFFERED=1 PIP_DISABLE_PIP_VERSION_CHECK=1
RUN apt-get update && apt-get install -y --no-install-recommends git curl ca-certificates build-essential ripgrep procps && rm -rf /var/lib/apt/lists/*
COPY --from=go /usr/local/go /usr/local/go
COPY --from=node /usr/local/bin/node /usr/local/bin/node
COPY --from=node /usr/local/lib/node_modules /usr/local/lib/node_modules
RUN ln -s ../lib/node_modules/npm/bin/npm-cli.js /usr/local/bin/npm && ln -s ../lib/node_modules/npm/bin/npx-cli.js /usr/local/bin/npx
ARG HERMES_REVISION=498abb677ec39ea3ae9f8f5ed60e7def6bc47e70
RUN git clone https://github.com/NousResearch/hermes-agent.git /opt/hermes && git -C /opt/hermes checkout --detach "$HERMES_REVISION"
COPY deploy/production/patch-hermes-runtime.py /tmp/patch.py
COPY deploy/isolation/requirements.lock /tmp/requirements.lock
RUN python /tmp/patch.py /opt/hermes && python -m venv /opt/venv && /opt/venv/bin/pip install -r /tmp/requirements.lock && /opt/venv/bin/pip install --no-deps -e /opt/hermes
COPY deploy/hermes-gateway/386gpt-channel /opt/channel
COPY deploy/isolation/start.py /opt/start.py
COPY deploy/isolation/patch-readonly-config.py /tmp/patch-readonly-config.py
RUN python /tmp/patch-readonly-config.py
RUN groupadd --gid 10001 hermes && useradd --uid 10001 --gid 10001 --home-dir /state/home --no-create-home hermes && mkdir -p /state/profile /state/home /workspace && chown -R 10001:10001 /state /workspace
ENV PATH=/opt/venv/bin:/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin HOME=/state/home HERMES_HOME=/state/profile PYTHONPATH=/opt/hermes API_SERVER_ENABLED=false HERMES_CONFIG_READONLY=1
USER 10001:10001
WORKDIR /workspace
ENTRYPOINT ["python", "/opt/start.py"]
