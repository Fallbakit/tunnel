# Fallbakit Tunnel Agent

Open-source customer-side tunnel agent for Fallbakit.

The agent runs near a customer's local runtime, opens an outbound WebSocket tunnel to Fallbakit, and forwards local-first inference requests to Ollama, oMLX, or vLLM without exposing that runtime to the internet.

## Quickstart

Use Ollama:

```sh
export FALLBAKIT_RUNNER_ID=runner_from_dashboard
export FALLBAKIT_RUNNER_API_KEY=rr_from_dashboard
export FALLBAKIT_BASE_URL=https://api.fallbakit.com
export FALLBAKIT_LOCAL_PROVIDER=ollama
export FALLBAKIT_LOCAL_BASE_URL=http://localhost:11434

fallbakit-agent
```

Use oMLX:

```sh
export FALLBAKIT_RUNNER_ID=runner_from_dashboard
export FALLBAKIT_RUNNER_API_KEY=rr_from_dashboard
export FALLBAKIT_BASE_URL=https://api.fallbakit.com
export FALLBAKIT_LOCAL_PROVIDER=omlx
export FALLBAKIT_LOCAL_BASE_URL=http://localhost:8000

fallbakit-agent
```

Use vLLM:

```sh
export FALLBAKIT_RUNNER_ID=runner_from_dashboard
export FALLBAKIT_RUNNER_API_KEY=rr_from_dashboard
export FALLBAKIT_BASE_URL=https://api.fallbakit.com
export FALLBAKIT_LOCAL_PROVIDER=vllm
export FALLBAKIT_LOCAL_BASE_URL=http://localhost:8000
export FALLBAKIT_LOCAL_API_KEY=optional_vllm_api_key

fallbakit-agent
```

When you call vLLM directly with an OpenAI client, use `http://localhost:8000/v1` as the client base URL. For the Fallbakit agent, set `FALLBAKIT_LOCAL_BASE_URL=http://localhost:8000` without `/v1`; Fallbakit adds `/v1/chat/completions` and `/v1/models`.

The agent calls `POST /v1/runners/bootstrap`, receives a short-lived tunnel session token, connects to `wss://api.fallbakit.com/tunnel`, and reconnects automatically with backoff when interrupted.

## Docker

Use a host runtime from a container:

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

For oMLX or vLLM, change `FALLBAKIT_LOCAL_PROVIDER` to `omlx` or `vllm` and point `FALLBAKIT_LOCAL_BASE_URL` at the reachable endpoint, for example `http://host.docker.internal:8000`. If vLLM is started with `--api-key`, also set `FALLBAKIT_LOCAL_API_KEY`.

## Docker Compose

```sh
export FALLBAKIT_RUNNER_ID=runner_from_dashboard
export FALLBAKIT_RUNNER_API_KEY=rr_from_dashboard
export FALLBAKIT_LOCAL_PROVIDER=ollama
export FALLBAKIT_LOCAL_BASE_URL=http://host.docker.internal:11434
docker compose up -d
```

To run Ollama in the same Compose project:

```sh
export FALLBAKIT_RUNNER_ID=runner_from_dashboard
export FALLBAKIT_RUNNER_API_KEY=rr_from_dashboard
export FALLBAKIT_LOCAL_PROVIDER=ollama
export FALLBAKIT_LOCAL_BASE_URL=http://ollama:11434
docker compose --profile with-ollama up -d
```

For oMLX or vLLM, keep the same agent service and point `FALLBAKIT_LOCAL_BASE_URL` at a reachable host or cluster service URL.

## Kubernetes

Start from `deployments/kubernetes/fallbakit-agent.yaml`, store the API key in the Secret, set `FALLBAKIT_LOCAL_PROVIDER`, set `FALLBAKIT_LOCAL_BASE_URL` to your runtime service, then apply:

```sh
kubectl apply -f deployments/kubernetes/fallbakit-agent.yaml
kubectl rollout status deployment/fallbakit-agent
```

## Configuration

CLI flags override environment variables. Environment variables override a YAML config file passed with `-config`.

| YAML key | Environment | Default |
| --- | --- | --- |
| `api_key` | `FALLBAKIT_RUNNER_API_KEY` | Required |
| `base_url` | `FALLBAKIT_BASE_URL` | `http://localhost:8080` |
| `local_provider` | `FALLBAKIT_LOCAL_PROVIDER` | `ollama` |
| `local_base_url` | `FALLBAKIT_LOCAL_BASE_URL` | `http://localhost:11434` for Ollama, `http://localhost:8000` for oMLX or vLLM |
| `local_api_key` | `FALLBAKIT_LOCAL_API_KEY` | Optional local runtime API key |
| `ollama_url` | `FALLBAKIT_OLLAMA_URL` | Deprecated Ollama-only alias |
| `runner_id` | `FALLBAKIT_RUNNER_ID` | Required |
| `agent_id` | `FALLBAKIT_AGENT_ID` | Hostname |
| `tunnel_url` | `FALLBAKIT_TUNNEL_URL` | Bootstrap response |
| `metrics_addr` | `FALLBAKIT_METRICS_ADDR` | Disabled |
| `log_format` | `FALLBAKIT_LOG_FORMAT` | `text` |
| `service_name` | `FALLBAKIT_SERVICE_NAME` | `fallbakit-agent` |
| `min_backoff` | `FALLBAKIT_MIN_BACKOFF` | `1s` |
| `max_backoff` | `FALLBAKIT_MAX_BACKOFF` | `30s` |
| `connect_timeout` | `FALLBAKIT_CONNECT_TIMEOUT` | `10s` |
| `local_timeout` | `FALLBAKIT_LOCAL_TIMEOUT` | `3s` |
| `ollama_timeout` | `FALLBAKIT_OLLAMA_TIMEOUT` | Deprecated alias for `local_timeout` |
| `update_url` | `FALLBAKIT_AGENT_UPDATE_URL` | Empty |
| `update_public_key` | `FALLBAKIT_AGENT_UPDATE_PUBLIC_KEY` | Empty |
| `update_check` | `FALLBAKIT_AGENT_UPDATE_CHECK` | `false` |

## Operations

When `metrics_addr` is set, the agent exposes:

| Path | Purpose |
| --- | --- |
| `/healthz` | Process liveness |
| `/readyz` | Tunnel connected and provider-specific readiness probe succeeds |
| `/metrics` | Prometheus metrics |

Readiness probes are provider-aware:

- Ollama: `GET /api/tags`
- oMLX: `GET /v1/models`
- vLLM: `GET /v1/models`

The agent keeps streaming paths unbuffered through the tunnel. Usage tracking, billing, provider credentials, and cloud fallback decisions stay in the hosted Fallbakit API, not in the customer agent.

## Security

- The runner API key is used only to request a short-lived tunnel session token.
- The tunnel token is bound to one account and runner.
- The agent never receives provider credentials or billing secrets.
- `FALLBAKIT_LOCAL_API_KEY` is sent only from the agent to the local runtime and is never sent in bootstrap metadata.
- Do not bake API keys into images. Use environment injection, Compose `.env`, Kubernetes Secrets, or your platform secret manager.

## Development

```sh
go test ./...
go run ./cmd/fallbakit-agent \
  -api-key=rr_from_dashboard \
  -base-url=http://localhost:8080 \
  -local-provider=ollama \
  -local-base-url=http://localhost:11434
```

Use `-local-provider=omlx -local-base-url=http://localhost:8000` for oMLX or `-local-provider=vllm -local-base-url=http://localhost:8000 -local-api-key="$FALLBAKIT_LOCAL_API_KEY"` for vLLM.

## Release

1. Update `agentVersion` in `cmd/fallbakit-agent/main.go` through release-time linker flags or a source version bump.
2. Run `go test ./...`.
3. Build binaries:

```sh
GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o dist/fallbakit-agent-linux-amd64 ./cmd/fallbakit-agent
GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags="-s -w" -o dist/fallbakit-agent-darwin-arm64 ./cmd/fallbakit-agent
```

4. Build and push the image:

```sh
docker build -t ghcr.io/fallbakit/fallbakit-agent:latest .
docker push ghcr.io/fallbakit/fallbakit-agent:latest
```

## License

[Apache-2.0](LICENSE).
