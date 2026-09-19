# mTLS Go Demo

This project demonstrates two Go services communicating over mutually authenticated TLS (mTLS) in Kubernetes. It generates two image variants (`full` and `stripped`) from a single Dockerfile, differing only in whether debug symbols are included.

**WARNING:** The `Dockerfile` generates private keys and bakes them directly into the images. This is strictly for simplifying this demo so no extra secret management is required. **Never do this in production!**

## Prerequisites

- Docker
- Kubernetes cluster (e.g., kind, minikube)
- Helm
- Task (https://taskfile.dev/) or Make
- `readelf` (optional, for local verification)

## Build and Push

Replace `myrepo/tls-demo` with your actual registry in the `Taskfile.yml` or `Makefile` or pass it as an argument.

```bash
task build-full
task build-stripped
task push REPO=myregistry.example.com/tls-demo
```

## Install the Chart

```bash
task install REPO=myregistry.example.com/tls-demo
```
This will start two pods (`service-a` and `service-b`) exchanging data over mTLS.

## Viewing Logs

You can verify they are communicating by checking the logs:

```bash
kubectl logs -l app.kubernetes.io/part-of=tls-demo -f
```
You will see log lines with metadata like `msg_id`, `bytes_in`, `latency_ms`, and `peer_cn`. **The actual payload is never logged by the application.**

## Switching Variants

The `full` variant contains the symbol table and DWARF debug info, making it easy for eBPF tools to hook into `crypto/tls` functions. 
The `stripped` variant has these removed, making Go TLS tracing much harder for eBPF tools.

To switch to the stripped version:
```bash
task use-stripped REPO=myregistry.example.com/tls-demo
```

To switch back to the full version:
```bash
task use-full REPO=myregistry.example.com/tls-demo
```

## Checking in Pixie

If you use Pixie to observe the cluster traffic:
1. Open the Pixie UI or CLI and look at the HTTP traffic for the namespace where `tls-demo` is installed.
2. When using the **`full`** variant, Pixie should be able to hook into the Go `crypto/tls` functions and show you the plaintext request and response bodies. You will clearly see the JSON payloads containing the `DEMO-SECRET-...` marker.
3. When using the **`stripped`** variant, Pixie's eBPF probes will likely fail to find the required TLS symbols. You should only see connection-level metrics (TCP) or encrypted byte streams, and the plaintext HTTP payloads will not be captured.

*(Note: The exact behavior depends on your Pixie version and cluster configuration, but this is the expected result for demonstrating the impact of stripped binaries on eBPF tracing).*

## Verification with readelf

The `Dockerfile` contains built-in checks to fail the build if the symbols are incorrect. You can also verify local binaries if you export them, using:

```bash
readelf -S <binary> | grep -E '\.symtab|\.debug_info'
```
