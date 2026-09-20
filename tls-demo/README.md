# mTLS Go Demo

This project demonstrates two Go services communicating over mutually authenticated TLS (mTLS) in Kubernetes. It generates two image variants (`full` and `stripped`) from a single Dockerfile, differing only in whether debug symbols are included.

**WARNING:** The `Dockerfile` generates private keys and bakes them directly into the images. This is strictly for simplifying this demo so no extra secret management is required. **Never do this in production!**

## Prerequisites

- Docker
- Kubernetes cluster (e.g., kind, minikube)
- Helm
- Make
- `readelf` (optional, for local verification)

## Values Files and Namespace

The Helm chart is deployed into the **`tls-demo`** namespace by default. Configuration overlay values files control which image variant is deployed:

- **`chart/tls-demo/values-full.yaml`**: Configures the Helm chart to deploy the `full` image variant (includes symbols and DWARF debug info).
- **`chart/tls-demo/values-stripped.yaml`**: Configures the Helm chart to deploy the `stripped` image variant (symbols removed via `-ldflags="-s -w"`).

## Connection Behavior

HTTP Keep-Alive is disabled on purpose (`DisableKeepAlives: true`). Each message sent between the services establishes a brand new TCP connection and performs a full TLS/mTLS handshake, generating a continuous stream of handshake events for testing and eBPF tracing.

## Makefile Targets

All operations are managed via the `Makefile`. You can override the default repository using `REPO=<your-registry>/tls-demo` and namespace using `NAMESPACE=<your-namespace>`.

- **`make build-full`**: Builds the `full` image variant.
- **`make build-stripped`**: Builds the `stripped` image variant.
- **`make push`**: Pushes both `full` and `stripped` image variants to the container registry.
- **`make install-full`**: Deploys or upgrades the `tls-demo` release in the `tls-demo` namespace using `chart/tls-demo/values-full.yaml`.
- **`make install-stripped`**: Deploys or upgrades the `tls-demo` release in the `tls-demo` namespace using `chart/tls-demo/values-stripped.yaml`.
- **`make use-full`**: Switches the active deployment to the `full` variant (depends on `install-full`).
- **`make use-stripped`**: Switches the active deployment to the `stripped` variant (depends on `install-stripped`).
- **`make template-full`**: Renders the Helm chart templates using `values-full.yaml` without installing.
- **`make template-stripped`**: Renders the Helm chart templates using `values-stripped.yaml` without installing.
- **`make uninstall`**: Uninstalls the `tls-demo` Helm release from the `tls-demo` namespace.

## Build and Deploy Workflow

1. Build the images:
   ```bash
   make build-full
   make build-stripped
   ```

2. Push the images to your registry:
   ```bash
   make push REPO=myregistry.example.com/tls-demo
   ```

3. Install the chart using either variant:
   ```bash
   make install-full REPO=myregistry.example.com/tls-demo
   ```
   or
   ```bash
   make install-stripped REPO=myregistry.example.com/tls-demo
   ```

4. Render templates for inspection (optional):
   ```bash
   make template-full REPO=myregistry.example.com/tls-demo
   make template-stripped REPO=myregistry.example.com/tls-demo
   ```

5. Uninstall when finished:
   ```bash
   make uninstall
   ```

## Viewing Logs

You can verify that the services are communicating by checking the logs:

```bash
kubectl logs -l app.kubernetes.io/part-of=tls-demo -n tls-demo -f
```
You will see log lines with metadata like `msg_id`, `bytes_in`, `latency_ms`, and `peer_cn`. **The actual payload is never logged by the application.**

## Switching Variants

- To switch to the stripped version:
  ```bash
  make use-stripped REPO=myregistry.example.com/tls-demo
  ```

- To switch back to the full version:
  ```bash
  make use-full REPO=myregistry.example.com/tls-demo
  ```

## Stripped Binaries and .gopclntab

Note that Go stripped binaries (compiled with `-ldflags="-s -w"`) still retain the `.gopclntab` section in the binary structure, which contains function name mappings required by the Go runtime for panic traces. Because `.gopclntab` is present, the capability of an eBPF tool to extract symbols or trace TLS functions depends on the specific eBPF tool and its version (this variability is expected, not guaranteed).

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
