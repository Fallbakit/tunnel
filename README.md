# Fallbakit Tunnel Agent

Open-source, customer-side tunnel agent for [Fallbakit](https://fallbakit.com).

The agent runs next to your local model runtime, opens an **outbound** WebSocket
tunnel to Fallbakit, and forwards local-first inference requests to Ollama, oMLX,
or vLLM — **without exposing that runtime to the internet**. No inbound ports, no
public IP, no reverse proxy.

[![Release](https://img.shields.io/github/v/release/Fallbakit/tunnel)](https://github.com/Fallbakit/tunnel/releases)

---

## Contents

- [How it works](#how-it-works)
- [Requirements](#requirements)
- [Install](#install)
- [Quickstart](#quickstart)
- [Configuration](#configuration)
  - [Precedence](#precedence)
  - [Full reference](#full-reference)
  - [YAML config file](#yaml-config-file)
- [Local providers](#local-providers)
- [Deployment](#deployment)
  - [Docker](#docker)
  - [Docker Compose](#docker-compose)
  - [Kubernetes](#kubernetes)
  - [systemd](#systemd)
- [Operations](#operations)
  - [Health, readiness, metrics](#health-readiness-metrics)
  - [Logging and tracing](#logging-and-tracing)
- [Security model](#security-model)
- [Signed auto-updates](#signed-auto-updates)
- [Troubleshooting](#troubleshooting)
- [FAQ](#faq)
- [Development](#development)
- [Release](#release)
- [License](#license)

---

## How it works

```
                          (1) POST /v1/runners/bootstrap   Authorization: Bearer rr_…
   fallbakit-agent  ───────────────────────────────────────────────▶  Fallbakit API
        │                                                                   │
        │           (2) { tunnel_url, session_token (HMAC, ~15m TTL), … }   │
        │  ◀────────────────────────────────────────────────────────────────┘
        │
        │           (3) dial wss://…/tunnel   Authorization: Bearer <session_token>
        │  ─────────────────────────────────────────────────────────────▶  Fallbakit edge
        │                                                                   │
        │           (4) yamux-multiplexed streams: server opens a stream    │
        │               per inference request                               │
        │  ◀────────────────────────────────────────────────────────────────┘
        │
        ▼  (5) forward each stream as a plain HTTP request
   Ollama / oMLX / vLLM  (e.g. http://localhost:11434)
```

1. **Bootstrap.** The agent calls `POST /v1/runners/bootstrap` with the runner API
   key (`rr_…`) and metadata (runner id, agent id, version, hostname, local
   provider/base URL, labels).
2. **Session token.** Fallbakit returns a short-lived, HMAC-SHA256-signed session
   token (default TTL ~15 minutes) bound to one account and runner, plus the
   tunnel URL.
3. **Connect.** The agent dials `wss://…/tunnel` using the session token as a
   bearer credential. If the bootstrap response has no tunnel URL, it's derived
   from the base URL (`https`→`wss`, `http`→`ws`, path `/tunnel`).
4. **Multiplex.** The connection is a [yamux](https://github.com/hashicorp/yamux)
   session. Fallbakit opens one stream per inference request; the agent reads each
   as an HTTP request.
5. **Forward.** The agent forwards the request to your local runtime, optionally
   injecting `FALLBAKIT_LOCAL_API_KEY` as a bearer token, and streams the response
   straight back through the tunnel (streaming paths stay unbuffered). If the
   local runtime fails, the agent returns `502 Bad Gateway` with an
   `X-Fallbakit-Tunnel-Error` header.

The agent **reconnects automatically** with exponential backoff plus jitter
(`min_backoff`…`max_backoff`) whenever bootstrap, dial, or the session is
interrupted. Usage tracking, billing, provider credentials, and cloud fallback
decisions all stay in the hosted Fallbakit API — never in the agent.

## Requirements

- A Fallbakit account, a **runner** created in the [dashboard](https://fallbakit.com)
  (gives you `FALLBAKIT_RUNNER_ID` and an `rr_…` runner API key).
- A local model runtime reachable from the agent: Ollama, oMLX, or vLLM.
- One of: the prebuilt binary, Docker, or Go 1.25+ to build from source.

## Install

### Prebuilt binary (recommended)

Download the archive for your OS/arch from the
[releases page](https://github.com/Fallbakit/tunnel/releases) and put
`fallbakit-agent` on your `PATH`. Builds are published for `linux` and `darwin`,
`amd64` and `arm64`. A `checksums.txt` is published alongside — verify it:

```sh
# Example: pick the asset matching your platform, then:
tar -xzf fallbakit-agent_<version>_<os>_<arch>.tar.gz
sha256sum -c checksums.txt --ignore-missing
sudo mv fallbakit-agent /usr/local/bin/
fallbakit-agent -h
```

### Docker

```sh
docker pull ghcr.io/fallbakit/fallbakit-agent:latest
```

The image is a distroless, non-root static binary. See [Docker](#docker) for run
commands.

### Build from source

```sh
git clone https://github.com/Fallbakit/tunnel.git
cd tunnel
go build -trimpath -ldflags="-s -w" -o fallbakit-agent ./cmd/fallbakit-agent
./fallbakit-agent -h
```

## Quickstart

Create a runner in the dashboard, then export its values and start the agent. Pick
your local provider:

**Ollama** (default, expects `http://localhost:11434`):

```sh
export FALLBAKIT_RUNNER_ID=runner_from_dashboard
export FALLBAKIT_RUNNER_API_KEY=rr_from_dashboard
export FALLBAKIT_BASE_URL=https://api.fallbakit.com
export FALLBAKIT_LOCAL_PROVIDER=ollama
export FALLBAKIT_LOCAL_BASE_URL=http://localhost:11434

fallbakit-agent
```

**oMLX** (expects `http://localhost:8000`):

```sh
export FALLBAKIT_RUNNER_ID=runner_from_dashboard
export FALLBAKIT_RUNNER_API_KEY=rr_from_dashboard
export FALLBAKIT_BASE_URL=https://api.fallbakit.com
export FALLBAKIT_LOCAL_PROVIDER=omlx
export FALLBAKIT_LOCAL_BASE_URL=http://localhost:8000

fallbakit-agent
```

**vLLM** (expects `http://localhost:8000`, optional API key):

```sh
export FALLBAKIT_RUNNER_ID=runner_from_dashboard
export FALLBAKIT_RUNNER_API_KEY=rr_from_dashboard
export FALLBAKIT_BASE_URL=https://api.fallbakit.com
export FALLBAKIT_LOCAL_PROVIDER=vllm
export FALLBAKIT_LOCAL_BASE_URL=http://localhost:8000
export FALLBAKIT_LOCAL_API_KEY=optional_vllm_api_key

fallbakit-agent
```

> **vLLM base URL gotcha:** when you call vLLM directly with an OpenAI client you
> use `http://localhost:8000/v1`. For the agent, set
> `FALLBAKIT_LOCAL_BASE_URL=http://localhost:8000` **without** `/v1` — Fallbakit
> appends `/v1/chat/completions` and `/v1/models` itself.

On start you'll see a structured log line with the resolved provider, base URL,
runner/agent ids, and whether tracing is enabled. Successful operation is silent
except for reconnect/error logs.

## Configuration

The agent is configured by **CLI flags**, **environment variables**, and an
optional **YAML file** — pick whichever suits your deployment.

### Precedence

```
CLI flags  >  environment variables  >  YAML config file  >  built-in defaults
```

Point at a YAML file with `-config <path>` or `FALLBAKIT_AGENT_CONFIG=<path>`.

### Full reference

| YAML key | Environment | CLI flag | Default | Purpose |
|---|---|---|---|---|
| `api_key` | `FALLBAKIT_RUNNER_API_KEY` | `-api-key` | **required** | Runner key (`rr_…`) for bootstrap. (`FALLBAKIT_API_KEY` is accepted as a fallback.) |
| `runner_id` | `FALLBAKIT_RUNNER_ID` | `-runner-id` | **required** | Dashboard-generated runner id. |
| `base_url` | `FALLBAKIT_BASE_URL` | `-base-url` | `http://localhost:8080` | Fallbakit platform origin. |
| `tunnel_url` | `FALLBAKIT_TUNNEL_URL` | `-tunnel-url` | from bootstrap | Explicit WebSocket tunnel URL (overrides bootstrap). |
| `local_provider` | `FALLBAKIT_LOCAL_PROVIDER` | `-local-provider` | `ollama` | `ollama`, `omlx`, or `vllm`. |
| `local_base_url` | `FALLBAKIT_LOCAL_BASE_URL` | `-local-base-url` | `:11434` (ollama) / `:8000` (omlx, vllm) | Local runtime origin (no `/v1`). |
| `local_api_key` | `FALLBAKIT_LOCAL_API_KEY` | `-local-api-key` | empty | Forwarded **only** agent→local runtime. |
| `agent_id` | `FALLBAKIT_AGENT_ID` | `-agent-id` | hostname | Stable agent id in tunnel metadata. |
| `metrics_addr` | `FALLBAKIT_METRICS_ADDR` | `-metrics-addr` | disabled | Listen addr for health/metrics, e.g. `:9093`. |
| `log_format` | `FALLBAKIT_LOG_FORMAT` | `-log-format` | `text` | `text` or `json`. |
| `service_name` | `FALLBAKIT_SERVICE_NAME` | `-service-name` | `fallbakit-agent` | Name for logs, metrics, traces. |
| `otel_endpoint` | `FALLBAKIT_OTEL_ENDPOINT` | `-otel-endpoint` | empty | OTLP/HTTP trace endpoint `host:port`. |
| `min_backoff` | `FALLBAKIT_MIN_BACKOFF` | `-min-backoff` | `1s` | Minimum reconnect backoff. |
| `max_backoff` | `FALLBAKIT_MAX_BACKOFF` | `-max-backoff` | `30s` | Maximum reconnect backoff. |
| `connect_timeout` | `FALLBAKIT_CONNECT_TIMEOUT` | `-connect-timeout` | `10s` | Bootstrap HTTP timeout. |
| `local_timeout` | `FALLBAKIT_LOCAL_TIMEOUT` | `-local-timeout` | `3s` | Local readiness-probe timeout. |
| `update_url` | `FALLBAKIT_AGENT_UPDATE_URL` | `-update-url` | empty | Signed update manifest URL. |
| `update_public_key` | `FALLBAKIT_AGENT_UPDATE_PUBLIC_KEY` | `-update-public-key` | empty | Base64/hex Ed25519 update key. |
| `update_check` | `FALLBAKIT_AGENT_UPDATE_CHECK` | `-update-check` | `false` | Verify + stage a signed update before connecting. |
| `ollama_url` | `FALLBAKIT_OLLAMA_URL` | `-ollama` | — | **Deprecated** Ollama-only alias for `local_base_url`. |
| `ollama_timeout` | `FALLBAKIT_OLLAMA_TIMEOUT` | `-ollama-timeout` | — | **Deprecated** alias for `local_timeout`. |
| — | `FALLBAKIT_AGENT_CONFIG` | `-config` | empty | Path to the YAML config file. |

Durations use Go syntax (`1s`, `500ms`, `2m`). Boolean envs accept `1`, `true`,
`TRUE`, or `yes`.

### YAML config file

```yaml
# agent.yaml — CLI flags override env, env overrides this file.
api_key: "rr_from_dashboard"
runner_id: "runner_from_dashboard"
base_url: "https://api.fallbakit.com"

local_provider: "ollama"          # ollama, omlx, or vllm
local_base_url: "http://localhost:11434"
local_api_key: ""                 # forwarded only to the local runtime

agent_id: "workstation-01"
tunnel_url: ""                    # leave empty to use the bootstrap URL

metrics_addr: ":9093"
log_format: "json"
service_name: "fallbakit-agent"
min_backoff: "1s"
max_backoff: "30s"
connect_timeout: "10s"
local_timeout: "3s"

update_url: ""
update_public_key: ""
update_check: false
```

```sh
fallbakit-agent -config agent.yaml
```

A ready-to-edit copy lives at [`configs/agent.example.yaml`](configs/agent.example.yaml).

## Local providers

| Provider | Default base URL | Readiness probe | Notes |
|---|---|---|---|
| `ollama` | `http://localhost:11434` | `GET /api/tags` | Default provider. |
| `omlx` | `http://localhost:8000` | `GET /v1/models` | OpenAI-compatible. |
| `vllm` | `http://localhost:8000` | `GET /v1/models` | Set `local_api_key` if vLLM was started with `--api-key`. |

Always set `local_base_url` to the runtime **origin** (no `/v1`). The agent and
the readiness probe append the right paths per provider.

## Deployment

### Docker

Reach a runtime on the host from inside the container with
`host.docker.internal`:

```sh
docker run --rm \
  -e FALLBAKIT_RUNNER_ID=runner_from_dashboard \
  -e FALLBAKIT_RUNNER_API_KEY=rr_from_dashboard \
  -e FALLBAKIT_BASE_URL=https://api.fallbakit.com \
  -e FALLBAKIT_LOCAL_PROVIDER=ollama \
  -e FALLBAKIT_LOCAL_BASE_URL=http://host.docker.internal:11434 \
  -e FALLBAKIT_METRICS_ADDR=:9093 \
  -p 9093:9093 \
  --add-host=host.docker.internal:host-gateway \
  ghcr.io/fallbakit/fallbakit-agent:latest
```

For oMLX or vLLM, change `FALLBAKIT_LOCAL_PROVIDER` and point
`FALLBAKIT_LOCAL_BASE_URL` at the reachable endpoint (e.g.
`http://host.docker.internal:8000`). If vLLM uses `--api-key`, also set
`FALLBAKIT_LOCAL_API_KEY`.

### Docker Compose

Using a host runtime:

```sh
export FALLBAKIT_RUNNER_ID=runner_from_dashboard
export FALLBAKIT_RUNNER_API_KEY=rr_from_dashboard
export FALLBAKIT_LOCAL_PROVIDER=ollama
export FALLBAKIT_LOCAL_BASE_URL=http://host.docker.internal:11434
docker compose up -d
```

Or run Ollama inside the same project with the bundled profile:

```sh
export FALLBAKIT_RUNNER_ID=runner_from_dashboard
export FALLBAKIT_RUNNER_API_KEY=rr_from_dashboard
export FALLBAKIT_LOCAL_PROVIDER=ollama
export FALLBAKIT_LOCAL_BASE_URL=http://ollama:11434
docker compose --profile with-ollama up -d
```

See [`docker-compose.yml`](docker-compose.yml). Variables can come from a
`.env` file (see [`.env.example`](.env.example)).

### Kubernetes

A complete manifest — Secret, ConfigMap, Deployment (with liveness/readiness
probes and resource requests), and a metrics Service — is at
[`deployments/kubernetes/fallbakit-agent.yaml`](deployments/kubernetes/fallbakit-agent.yaml).

```sh
# 1. Put the rr_… key in the Secret's runner-api-key field.
# 2. Set FALLBAKIT_RUNNER_ID, FALLBAKIT_LOCAL_PROVIDER, and
#    FALLBAKIT_LOCAL_BASE_URL (your runtime Service URL) in the ConfigMap.
kubectl apply -f deployments/kubernetes/fallbakit-agent.yaml
kubectl rollout status deployment/fallbakit-agent
```

The Deployment defaults to `replicas: 1`. Probes hit `/healthz` (liveness) and
`/readyz` (readiness) on the metrics port.

### systemd

For a bare-metal/VM install of the binary, run it as a service. Put config in an
environment file so secrets stay out of the unit:

```ini
# /etc/fallbakit/agent.env
FALLBAKIT_RUNNER_ID=runner_from_dashboard
FALLBAKIT_RUNNER_API_KEY=rr_from_dashboard
FALLBAKIT_BASE_URL=https://api.fallbakit.com
FALLBAKIT_LOCAL_PROVIDER=ollama
FALLBAKIT_LOCAL_BASE_URL=http://localhost:11434
FALLBAKIT_METRICS_ADDR=:9093
FALLBAKIT_LOG_FORMAT=json
```

```ini
# /etc/systemd/system/fallbakit-agent.service
[Unit]
Description=Fallbakit Tunnel Agent
After=network-online.target
Wants=network-online.target

[Service]
EnvironmentFile=/etc/fallbakit/agent.env
ExecStart=/usr/local/bin/fallbakit-agent
Restart=always
RestartSec=2
DynamicUser=yes
NoNewPrivileges=yes

[Install]
WantedBy=multi-user.target
```

```sh
sudo chmod 600 /etc/fallbakit/agent.env
sudo systemctl daemon-reload
sudo systemctl enable --now fallbakit-agent
journalctl -u fallbakit-agent -f
```

## Operations

### Health, readiness, metrics

When `metrics_addr` is set (e.g. `:9093`), the agent serves:

| Path | Meaning |
|---|---|
| `/healthz` | Process liveness — always `200 ok` while running. |
| `/readyz` | `200 ready` only when the tunnel is connected **and** the provider readiness probe succeeds; otherwise `503` with the reason. |
| `/metrics` | Prometheus metrics. |

`/readyz` is provider-aware — it probes `GET /api/tags` for Ollama and
`GET /v1/models` for oMLX/vLLM, with a timeout of `local_timeout`. Use `/readyz`
for orchestrator readiness gating and `/healthz` for liveness.

### Logging and tracing

- **`log_format`** — `text` (default, human-readable) or `json` (structured, for
  log pipelines). Logs include the resolved provider, base URL, and connection
  state changes.
- **`service_name`** — labels logs, metrics, and traces.
- **`otel_endpoint`** — set an OTLP/HTTP `host:port` to export traces. Leave empty
  to disable tracing (the default; no exporter is started).

Sensitive values (keys, tokens) are kept out of logs and the `502` error header is
sanitized of control characters.

## Security model

- The agent connects **outbound only**. Your local runtime needs no inbound ports
  and is never exposed to the internet.
- The runner API key (`rr_…`) is used **only** to request a short-lived tunnel
  session token. That token is HMAC-signed, time-limited (~15m), and bound to one
  account and runner.
- The agent **never** receives provider credentials, billing secrets, or other
  customers' data. Fallback/billing/usage all live in the hosted API.
- `FALLBAKIT_LOCAL_API_KEY` is sent **only** from the agent to your local runtime.
  It is never included in bootstrap metadata or sent to Fallbakit.
- **Don't bake keys into images.** Use environment injection, Compose `.env`,
  Kubernetes Secrets, or your platform secret manager. Lock down the env file
  (`chmod 600`) on bare metal.
- The Docker image runs as a non-root distroless static binary.

## Signed auto-updates

Auto-update is **opt-in** and verification-first. When enabled, before connecting
the agent fetches a manifest from `update_url`, verifies its Ed25519 signature
against `update_public_key`, and atomically replaces its own binary on disk.

```sh
fallbakit-agent \
  -update-check \
  -update-url=https://example.com/fallbakit-agent/manifest.json \
  -update-public-key=BASE64_OR_HEX_ED25519_PUBLIC_KEY
```

Restarting the process to pick up the staged binary remains the responsibility of
your supervisor (Docker, Kubernetes, or systemd). A failed or unverifiable update
is logged as a warning and the agent continues running its current version.

## Troubleshooting

| Symptom | Likely cause / fix |
|---|---|
| Exits with `runner api-key is required` / `runner id is required` (exit code 2) | Set `FALLBAKIT_RUNNER_API_KEY` and `FALLBAKIT_RUNNER_ID`. |
| `tunnel bootstrap failed with status 401/403` | Bad/disabled runner key, or wrong key type. Use the `rr_…` **runner** key (not an `or_…` application key). |
| `invalid local base URL` (exit code 2) | `local_base_url` must include scheme and host, e.g. `http://localhost:11434`. |
| `unsupported local provider` (exit code 2) | `local_provider` must be `ollama`, `omlx`, or `vllm`. |
| `FALLBAKIT_OLLAMA_URL … only supported with local provider "ollama"` | The deprecated `-ollama`/`FALLBAKIT_OLLAMA_URL` alias is Ollama-only. Use `FALLBAKIT_LOCAL_BASE_URL` for oMLX/vLLM. |
| Connects, but `/readyz` returns `503 … unavailable` | Tunnel up but the local runtime probe failed. Confirm the runtime is running and reachable at `local_base_url`. |
| `/readyz` returns `503 tunnel disconnected` | Bootstrap or dial is failing — check `base_url`, network egress to Fallbakit, and the runner key. The agent retries with backoff. |
| Requests return `502 Bad Gateway` (`X-Fallbakit-Tunnel-Error`) | The local runtime rejected or failed the forwarded request. The header carries the cause. |
| vLLM returns `401` | vLLM started with `--api-key`; set `FALLBAKIT_LOCAL_API_KEY`. |
| Container can't reach host runtime | Use `host.docker.internal` + `--add-host=host.docker.internal:host-gateway` (Linux). |
| Constant reconnects | Inspect logs (`log_format: json`) for the underlying bootstrap/dial error; verify clock skew isn't past the token TTL. |

## FAQ

**Do I need to open a firewall port?** No. The agent dials out over WebSocket
(`wss`). Nothing listens for inbound connections except the optional local metrics
port.

**Can one agent serve multiple providers?** No — one agent forwards to one
`local_base_url`. Run one agent (and one runner) per local runtime.

**Can I run several agents for the same account?** Yes, each with its own runner.
The bootstrap response includes a `runner_limit` for your account.

**Does it buffer streaming responses?** No. Streaming responses pass through
unbuffered, so token-by-token output reaches Fallbakit as the runtime produces it.

**What happens to in-flight requests on reconnect?** The session closes and the
agent re-bootstraps and redials with backoff; new requests resume once `/readyz`
is green.

**Where do binaries and images come from?** Binaries are published to
[GitHub Releases](https://github.com/Fallbakit/tunnel/releases) via GoReleaser;
images to `ghcr.io/fallbakit/fallbakit-agent`.

## Development

```sh
go test ./...

go run ./cmd/fallbakit-agent \
  -api-key=rr_from_dashboard \
  -runner-id=runner_from_dashboard \
  -base-url=http://localhost:8080 \
  -local-provider=ollama \
  -local-base-url=http://localhost:11434
```

For oMLX use `-local-provider=omlx -local-base-url=http://localhost:8000`; for
vLLM use `-local-provider=vllm -local-base-url=http://localhost:8000 -local-api-key="$FALLBAKIT_LOCAL_API_KEY"`.

Layout:

```
cmd/fallbakit-agent/   # CLI entrypoint, flag/env/YAML wiring, metrics server
internal/tunnel/       # bootstrap, dial, yamux session, request forwarding
internal/observability # logging, tracing, redaction
internal/update/       # signed update fetch/verify/atomic-replace
deployments/kubernetes # manifest
configs/               # agent.example.yaml
```

## Release

Releases are driven by GoReleaser and a tag push (`v*`); see
[`RELEASE.md`](RELEASE.md) and [`.github/workflows/release.yml`](.github/workflows/release.yml).
In short:

1. Run `go test ./...`.
2. Tag the repo `vX.Y.Z`. The release workflow builds Linux/macOS (amd64/arm64)
   binaries with checksums via GoReleaser and pushes
   `ghcr.io/fallbakit/fallbakit-agent:<version>` and `:latest`.
3. `agentVersion` is stamped at build time via
   `-ldflags "-X main.agentVersion=<version>"`.
4. When auto-update is enabled, sign update manifests with the Fallbakit Ed25519
   release key.

Manual build/push, if needed:

```sh
GOOS=linux  GOARCH=amd64 go build -trimpath -ldflags="-s -w -X main.agentVersion=$VERSION" -o dist/fallbakit-agent-linux-amd64  ./cmd/fallbakit-agent
GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags="-s -w -X main.agentVersion=$VERSION" -o dist/fallbakit-agent-darwin-arm64 ./cmd/fallbakit-agent

docker build -t ghcr.io/fallbakit/fallbakit-agent:latest .
docker push ghcr.io/fallbakit/fallbakit-agent:latest
```

## License

[Apache-2.0](LICENSE).
