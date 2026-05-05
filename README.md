<div align="center">
    <h1>MulVAL (as a Service)</h1>
    <p><b>Attack graph generation over gRPC, backed by first-order Datalog inference</b></p>
    <a href=""><img src="https://img.shields.io/github/license/cvewatcher/mulval?style=for-the-badge" alt="License"></a>
    <a href="https://pkg.go.dev/github.com/cvewatcher/mulval"><img src="https://shields.io/badge/-reference-blue?logo=go&style=for-the-badge" alt="Go reference"></a>
    <br>
    <a href="https://github.com/cvewatcher/mulval/actions?query=workflow%3Aci"><img src="https://img.shields.io/github/actions/workflow/status/cvewatcher/mulval/ci.yaml?style=for-the-badge&label=CI" alt="CI"></a>
    <a href="https://hub.docker.com/r/cvewatcher/mulval"><img src="https://img.shields.io/docker/pulls/cvewatcher/mulval?style=for-the-badge" alt="Docker pulls"></a>
</div>

MulVAL (as a Service) wraps [MulVAL](http://people.cs.ksu.edu/~xou/mulval/) — a logic-based network security analyser — behind a gRPC/REST API to distribute its capabilities to third-party services.
Clients submit EDB facts and optional IDB interaction rules; the service runs MulVAL in the background and returns a Long-Running Operation (LRO) that resolves to the generated attack graph.

> [!NOTE]
> This project is research-grade software.
> Changes can happen at any time in a non-backward-compatible way.

## 🚀 What MulVal (as a Service) Does

- **Simplify MulVal usage** — Running MulVal can become quite complex depending on your infrastructure, so running it as a Service helps bootstrapping it fast to experiment;
- **Manages multi-experiments** — Every analysis is stored so can be shared among researchers and engineers in a lab, or used to iterate;
- **Reusability** — Designed as a microservice, MulVal (as a Service) provides a gRPC/HTTP REST API that can be used by third-party services to run their experiment;
- **Visualization** — Provide a UI that maps the API features for running in-browser experiments and visualize the results.

## 🧩 Architecture

<div align="center">
    <img src="res/architecture.excalidraw.png" width="400px">
</div>

The service is stateless between requests; all durable state lives in PostgreSQL.
NATS JetStream is used only for completion notifications — `WaitOperation` subscribes to a per-operation subject and blocks until the executor publishes a state change, avoiding poll loops.
If NATS is unavailable the service degrades gracefully: `WaitOperation` returns after its timeout with `done=false`.

## ⚡ Quick start

```bash
# Write the configuration file config.yaml
# An example:
#
# logLevel: info
# events:
#   url: nats://localhost:4222
#   instanceID:
#     from_env: HOSTNAME
# storage:
#   dsn: postgres://user:secret@localhost:5432/mulval-backend
#   schema: mulval
#   migrate: true
#   minConns: 4

# Start the MulVal (as a Service) Docker container.
# Add --ui for the web graphical interface.
docker run -p 8080:8080 -v config.yaml:/config.yaml cvewatcher/mulval:latest --config=/config.yaml --ui

# The service is now available at localhost:8080 (gRPC/HTTP REST)
# The web UI is at http://localhost:8080/ui/
```

### Submitting an analysis via curl

```bash
curl -s -X POST http://localhost:8080/api/v1/analyses \
  -H 'Content-Type: application/json' \
  -d '{
    "edbFacts": [
      "attackerLocated(internet).",
      "attackGoal(execCode(fileServer,_)).",
      "hacl(internet, webServer, tcp, 80).",
      "hacl(webServer, fileServer, tcp, 445).",
      "hacl(H, H, _, _).",
      "networkServiceInfo(webServer, httpd, tcp, 80, apache).",
      "vulExists(webServer, '\''CVE-2021-44228'\'', httpd).",
      "vulProperty('\''CVE-2021-44228'\'', remoteExploit, privEscalation).",
      "networkServiceInfo(fileServer, smb, tcp, 445, root).",
      "vulExists(fileServer, '\''CVE-2017-0144'\'', smb).",
      "vulProperty('\''CVE-2017-0144'\'', remoteExploit, privEscalation)."
    ]
  }'
```

This returns an LRO immediately:

```json
{
  "name": "operations/xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx",
  "done": false
}
```

Poll until complete:

```bash
curl -s -X POST http://localhost:8080/api/v1/operations/xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx:wait \
  -H 'Content-Type: application/json' \
  -d '{"timeout": "30s"}'
```

When `done: true`, the response contains the full `Analysis` resource including `verticesCsv`, `arcsCsv`, and the parsed graph.

## 🖼️ User Interface

<div align="center">
    <table>
        <thead>
            <tr><th style="text-align:center">Capture</th><th style="text-align:center">Detail</th></tr>
        </thead>
        <tbody>
            <tr><td><img src="res/analyses.png" width="600px"></td><td>All analyses MulVal (as a Service) ran.</td></tr>
            <tr><td><img src="res/analysis.png" width="600px"></td><td>The details of an analysis, with a graph display of the results.</td></tr>
            <tr><td><img src="res/create.png" width="600px"></td><td>The creation form.</td></tr>
        </tbody>
    </table>
</div>

## 🔨 Development setup

### OpenTelemetry

- Setup:
    ```bash
    export OTEL_EXPORTER_OTLP_ENDPOINT=dns://localhost:4317
    export OTEL_EXPORTER_OTLP_INSECURE=true
    export OTEL_EXPORTER_OTLP_PROTOCOL=grpc
    ```

### PostgreSQL

- Setup:
    ```bash
    docker run -d \
        --name postgres \
        -e POSTGRES_DB=mulval-backend \
        -e POSTGRES_USER=user \
        -e POSTGRES_PASSWORD=secret \
        -p 5432:5432 \
        postgres:16-alpine
    ```

- Teardown:
    ```bash
    docker rm -f postgres
    ```

- Adminer, for debug purposes:
    ```bash
    docker run -p 8082:8080 adminer
    ```
    You can connect with:
    - **System**: `PostgreSQL`
    - **Server**: The result of `echo "$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' postgres):5432"`
    - **Username**: `user`
    - **Password**: `secret`
    - **Database**: `mulval-backend`

### NATS JetStream

- Setup:
    ```bash
    docker run -d \
        --name nats-js \
        -p 4222:4222 \
        -p 8222:8222 \
        nats:latest -js -m 8222
    ```

- Teardown:
    ```bash
    docker rm -f nats-js
    ```
