# Pixie Vizier Go OTLP Trace Generator (`seed_go`)

A standalone Go generator that simulates Pixie/Vizier eBPF HTTP trace span exports using the official OpenTelemetry Go SDK (`go.opentelemetry.io/otel`).

It generates real OTLP Trace Spans containing HTTP request/response payloads (`req_body` and `resp_body`) and metadata (`http.method`, `http.status_code`, `net.peer.ip`, `k8s.pod.name`), sending them over gRPC to the Pixie Collector on port `4317`.

---

## Configuration Environment Variables

| Variable | Default | Description |
| :--- | :--- | :--- |
| `COLLECTOR_ENDPOINT` | `localhost:4317` | gRPC endpoint of the target Pixie Collector |
| `INSECURE` | `true` | Set to `true` to disable TLS (matches collector insecure port) |
| `SEND_INTERVAL_MS` | `3000` | Delay between span transmissions in milliseconds |

---

## How to Run Standalone

### 1. Start the Pixie Collector
Run the collector service first (either in Docker or locally):

```bash
# Option A: Container mode
cd pixie_collector
docker build -t pixie-collector:latest .
docker run --rm -p 5000:5000 -p 4317:4317 --name pixie-collector pixie-collector:latest

# Option B: Local Python mode
cd pixie_collector
./run_demo.sh
```

### 2. Run the Go Trace Generator
In a separate terminal, launch `seed_go`:

```bash
cd pixie_collector/seed_go

# Run directly using go run
go run main.go

# Or set custom collector endpoint (e.g. port-forwarded from k8s)
COLLECTOR_ENDPOINT="localhost:4317" SEND_INTERVAL_MS=2000 go run main.go
```

### 3. View Results
Open `http://localhost:5000` to see the real-time PII classification results for generated trace spans on the Flask dashboard.
