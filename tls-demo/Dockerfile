# syntax=docker/dockerfile:1
# Note: Base images (golang:1.22, alpine:3.19, static-debian12) are kept on Go 1.22 for compatibility with Pixie's Go TLS tracing, even though these image versions are past end of support.
FROM --platform=linux/amd64 alpine:3.19 AS certs
RUN apk add --no-cache openssl
ARG NAMESPACE=tls-demo
WORKDIR /certs

# Generate CA
RUN openssl req -x509 -newkey rsa:4096 -days 365 -nodes -keyout ca.key -out ca.crt -subj "/CN=DemoCA"

# Generate leaf certs for both services with correct KU/EKU and SANs using EC P-256
RUN for svc in service-a service-b; do \
      echo "[req]" > ext.cnf && \
      echo "distinguished_name = req_distinguished_name" >> ext.cnf && \
      echo "req_extensions = v3_req" >> ext.cnf && \
      echo "prompt = no" >> ext.cnf && \
      echo "[req_distinguished_name]" >> ext.cnf && \
      echo "CN = $svc" >> ext.cnf && \
      echo "[v3_req]" >> ext.cnf && \
      echo "keyUsage = critical, digitalSignature, keyEncipherment" >> ext.cnf && \
      echo "extendedKeyUsage = serverAuth, clientAuth" >> ext.cnf && \
      echo "subjectAltName = @alt_names" >> ext.cnf && \
      echo "[alt_names]" >> ext.cnf && \
      echo "DNS.1 = $svc" >> ext.cnf && \
      echo "DNS.2 = $svc.${NAMESPACE}" >> ext.cnf && \
      echo "DNS.3 = $svc.${NAMESPACE}.svc" >> ext.cnf && \
      echo "DNS.4 = $svc.${NAMESPACE}.svc.cluster.local" >> ext.cnf && \
      echo "DNS.5 = localhost" >> ext.cnf && \
      openssl ecparam -name prime256v1 -genkey -noout -out ${svc}.key && \
      openssl req -new -key ${svc}.key -out ${svc}.csr -config ext.cnf && \
      openssl x509 -req -in ${svc}.csr -CA ca.crt -CAkey ca.key -CAcreateserial -out ${svc}.crt -days 365 -extensions v3_req -extfile ext.cnf; \
    done

# Clean up non-runtime certificate files so only ca.crt, service-a.crt/.key, and service-b.crt/.key remain
RUN rm -f /certs/ca.key /certs/ca.srl /certs/*.csr /certs/ext.cnf

# Stage: full build
FROM --platform=linux/amd64 golang:1.22 AS build-full
RUN apt-get update && apt-get install -y binutils
WORKDIR /src
COPY app/ ./
# Normal build, keeps symbols
RUN CGO_ENABLED=0 go build -o /app main.go
# Verify symbol table exists: BOTH .symtab AND (.debug_info or .zdebug_info). Fail build if missing.
RUN readelf -S /app > /tmp/sections && \
    grep -q '\.symtab' /tmp/sections && \
    (grep -q '\.debug_info' /tmp/sections || grep -q '\.zdebug_info' /tmp/sections) || \
    (echo "ERROR: Required symbol sections (.symtab AND .debug_info/.zdebug_info) missing in full build!" && exit 1)

# Stage: stripped build
FROM --platform=linux/amd64 golang:1.22 AS build-stripped
RUN apt-get update && apt-get install -y binutils
WORKDIR /src
COPY app/ ./
# Stripped build
RUN CGO_ENABLED=0 go build -trimpath -buildvcs=false -ldflags="-s -w" -o /app main.go
# Verify symbol table is REMOVED. Fail build if present.
RUN readelf -S /app > /tmp/sections && \
    if grep -q -E '\.symtab|\.debug_info|\.zdebug_info' /tmp/sections; then echo "ERROR: Symbols present in stripped build!" && exit 1; fi

# Final Target: full
# Both variants MUST come from this identical build to share the identical CA from the "certs" stage.
# WARNING: Baking private keys into images is for this demo only. Never do this in production.
FROM --platform=linux/amd64 gcr.io/distroless/static-debian12:nonroot AS full
COPY --chown=nonroot:nonroot --from=certs /certs /certs
COPY --chown=nonroot:nonroot --from=build-full /app /app
ENTRYPOINT ["/app"]

# Final Target: stripped
FROM --platform=linux/amd64 gcr.io/distroless/static-debian12:nonroot AS stripped
COPY --chown=nonroot:nonroot --from=certs /certs /certs
COPY --chown=nonroot:nonroot --from=build-stripped /app /app
ENTRYPOINT ["/app"]

