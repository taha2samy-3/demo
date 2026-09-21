package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"
)

// PayloadGenerator builds one realistic (request, response) pair for a given
// message id. Adding traffic variety is a matter of writing one of these and
// appending it to payloadGenerators below — nothing else needs to change.
type PayloadGenerator func(id string) (reqBody any, respBody any)

// genericAck is the flat, no-information response reused by generators for
// which a real service would plausibly just acknowledge the request.
var genericAck = map[string]string{"status": "ok", "msg": "received"}

// newReqBody stamps the required "id" and a "kind" (used by the /ingest
// handler to find the matching generator again) onto a generator's own
// fields.
func newReqBody(id, kind string, fields map[string]any) map[string]any {
	body := map[string]any{"id": id, "kind": kind}
	for k, v := range fields {
		body[k] = v
	}
	return body
}

// --- Clean / benign generators ---

func genHealthCheck(id string) (any, any) {
	req := newReqBody(id, "health_check", map[string]any{
		"service": "frontend",
	})
	return req, genericAck
}

func genMetricsReport(id string) (any, any) {
	req := newReqBody(id, "metrics_report", map[string]any{
		"service": "checkoutservice",
	})
	resp := map[string]any{
		"cpu_utilization":    14.2,
		"memory_mb":          512,
		"active_connections": 89,
	}
	return req, resp
}

func genProductSearch(id string) (any, any) {
	req := newReqBody(id, "product_search", map[string]any{
		"query":     "wireless noise canceling headphones",
		"category":  "electronics",
		"max_price": 200,
	})
	resp := map[string]any{
		"items_found": 14,
		"page":        1,
		"total_pages": 2,
	}
	return req, resp
}

func genCartUpdate(id string) (any, any) {
	req := newReqBody(id, "cart_update", map[string]any{
		"item_id":  "item_9941",
		"quantity": 2,
		"currency": "USD",
	})
	return req, genericAck
}

// --- PII-rich generators, one distinct category each ---

func genUserRegistration(id string) (any, any) {
	req := newReqBody(id, "user_registration", map[string]any{
		"username":  "johndoe",
		"email":     "john.doe@acme-corp.com",
		"full_name": "John Doe",
		"phone":     "+1-555-0199",
	})
	resp := map[string]any{
		"status":  "created",
		"user_id": "usr_99812",
	}
	return req, resp
}

func genContactUpdate(id string) (any, any) {
	req := newReqBody(id, "contact_update", map[string]any{
		"contact_person": "Robert Johnson",
		"company":        "Microsoft",
		"email":          "rjohnson@microsoft.com",
		"phone":          "+44 20 7946 0912",
	})
	resp := map[string]any{
		"updated":   true,
		"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
	}
	return req, resp
}

func genCheckoutPayment(id string) (any, any) {
	req := newReqBody(id, "checkout_payment", map[string]any{
		"customer_name":   "Sarah Connor",
		"card_number":     "4532-1189-9021-4412",
		"billing_address": "742 Evergreen Terrace, Springfield, OR 97477",
		"amount_usd":      149.99,
	})
	resp := map[string]any{
		"transaction_id": "tx_" + id,
		"status":         "approved",
	}
	return req, resp
}

func genIdentityVerify(id string) (any, any) {
	req := newReqBody(id, "identity_verify", map[string]any{
		"applicant_name": "Alexander Hamilton",
		"ssn":            "123-45-6789",
		"tax_id":         "987-65-4321",
		"address":        "1600 Pennsylvania Ave NW, Washington, DC",
	})
	resp := map[string]any{
		"verification_status": "verified",
		"credit_score":        790,
	}
	return req, resp
}

func genMedicalRecord(id string) (any, any) {
	// PII lands in the response here (a lookup by an already-known patient
	// id), which is itself a realistic and usefully different shape.
	req := newReqBody(id, "medical_record", map[string]any{
		"patient_id": "p_4412",
		"doctor_id":  "doc_12",
	})
	resp := map[string]any{
		"patient_name": "Emily Davis",
		"hospital":     "Boston General Hospital",
		"diagnosis":    "Acute Bronchitis",
		"note":         "Prescribed Amoxicillin 500mg, review in 7 days",
	}
	return req, resp
}

func genSupportTicket(id string) (any, any) {
	req := newReqBody(id, "support_ticket", map[string]any{
		"submitted_by": "Michael Brown",
		"company":      "Google",
		"phone":        "555-867-5309",
		"city":         "San Francisco",
		"issue":        "Billing discrepancy on invoice #1002",
	})
	resp := map[string]any{
		"ticket_id":      "TICK-4419",
		"assigned_group": "support-tier2",
	}
	return req, resp
}

// payloadGenerators is the registry generatePayload() draws from. Append a
// new gen* function here to add more traffic variety.
var payloadGenerators = []PayloadGenerator{
	genHealthCheck,
	genMetricsReport,
	genProductSearch,
	genCartUpdate,
	genUserRegistration,
	genContactUpdate,
	genCheckoutPayment,
	genIdentityVerify,
	genMedicalRecord,
	genSupportTicket,
}

// payloadGeneratorsByKind lets the /ingest handler regenerate a
// shape-appropriate response for a request without needing any state passed
// over the wire beyond the "kind" the generator stamped into the request.
var payloadGeneratorsByKind = buildPayloadGeneratorsByKind()

func buildPayloadGeneratorsByKind() map[string]PayloadGenerator {
	byKind := make(map[string]PayloadGenerator, len(payloadGenerators))
	for _, gen := range payloadGenerators {
		reqBody, _ := gen("lookup-probe")
		if m, ok := reqBody.(map[string]any); ok {
			if kind, ok := m["kind"].(string); ok {
				byKind[kind] = gen
			}
		}
	}
	return byKind
}

func main() {
	svcName := os.Getenv("SERVICE_NAME")
	if svcName == "" {
		svcName = "service-unknown"
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil)).With("service", svcName)

	peerAddr := os.Getenv("PEER_ADDR")
	if peerAddr == "" {
		logger.Error("PEER_ADDR is required")
		os.Exit(1)
	}

	peerHost, _, err := net.SplitHostPort(peerAddr)
	if err != nil {
		logger.Error("invalid PEER_ADDR format, expected host:port", "peer_addr", peerAddr, "error", err)
		os.Exit(1)
	}

	listenAddr := os.Getenv("LISTEN_ADDR")
	if listenAddr == "" {
		listenAddr = ":8443"
	}
	intervalSecs := 5
	if v := os.Getenv("SEND_INTERVAL_SECONDS"); v != "" {
		if i, err := strconv.Atoi(v); err == nil && i > 0 {
			intervalSecs = i
		} else {
			logger.Warn("invalid SEND_INTERVAL_SECONDS, must be greater than 0; using default 5", "value", v)
			intervalSecs = 5
		}
	}

	certDir := "/certs"
	caPath := filepath.Join(certDir, "ca.crt")
	certPath := filepath.Join(certDir, svcName+".crt")
	keyPath := filepath.Join(certDir, svcName+".key")

	caBytes, err := os.ReadFile(caPath)
	if err != nil {
		logger.Error("failed to read CA cert", "error", err)
		os.Exit(1)
	}
	caPool := x509.NewCertPool()
	if !caPool.AppendCertsFromPEM(caBytes) {
		logger.Error("failed to append CA cert")
		os.Exit(1)
	}

	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		logger.Error("failed to load key pair", "error", err)
		os.Exit(1)
	}

	// 1. Server setup
	serverTLSConfig := &tls.Config{
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    caPool,
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
		NextProtos:   []string{"http/1.1"}, // Disable HTTP/2 negotiation
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/ingest", func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1 MiB limit
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "Bad request", http.StatusBadRequest)
			return
		}

		peerCN := "unknown"
		if r.TLS != nil && len(r.TLS.PeerCertificates) > 0 {
			peerCN = r.TLS.PeerCertificates[0].Subject.CommonName
		}

		// parse partial id/kind for logging and to shape the response
		var reqData struct {
			ID   string `json:"id"`
			Kind string `json:"kind"`
		}
		if err := json.Unmarshal(body, &reqData); err != nil {
			logger.Warn("invalid JSON payload received", "peer_cn", peerCN, "error", err)
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}

		// Regenerate the matching generator's response shape rather than
		// trying to pass state across the network.
		var respBody any = genericAck
		if gen, ok := payloadGeneratorsByKind[reqData.Kind]; ok {
			_, respBody = gen(reqData.ID)
		}
		respBytes, _ := json.Marshal(respBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(respBytes)

		logger.Info("Ingest server handled request",
			"peer_cn", peerCN,
			"msg_id", reqData.ID,
			"bytes_in", len(body),
			"bytes_out", len(respBytes),
			"status", http.StatusOK,
			"latency_ms", time.Since(start).Milliseconds(),
		)
	})

	server := &http.Server{
		Addr:         listenAddr,
		Handler:      mux,
		TLSConfig:    serverTLSConfig,
		TLSNextProto: make(map[string]func(*http.Server, *tls.Conn, http.Handler)), // Force HTTP/1.1 internally
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  30 * time.Second,
	}

	// 2. Client setup
	clientTLSConfig := &tls.Config{
		RootCAs:      caPool,
		Certificates: []tls.Certificate{cert},
		ServerName:   peerHost,
		MinVersion:   tls.VersionTLS12,
		NextProtos:   []string{"http/1.1"}, // Disable HTTP/2 negotiation
	}

	tr := &http.Transport{
		TLSClientConfig:   clientTLSConfig,
		DisableKeepAlives: true,
		ForceAttemptHTTP2: false,
		TLSNextProto:      make(map[string]func(string, *tls.Conn) http.RoundTripper), // Force HTTP/1.1 internally
	}
	client := &http.Client{
		Transport: tr,
		Timeout:   5 * time.Second,
	}

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup

	// Start server routine
	wg.Add(1)
	go func() {
		defer wg.Done()
		logger.Info("Starting mTLS server", "addr", listenAddr)
		if err := server.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
			logger.Error("server failed", "error", err)
		}
	}()

	// Start client loop routine
	wg.Add(1)
	go func() {
		defer wg.Done()
		sendURL := "https://" + peerAddr + "/ingest"
		logger.Info("Starting mTLS client loop", "target", sendURL, "interval", intervalSecs)

		// Wait until peer is up
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}
			if err := pingPeer(client, sendURL); err != nil {
				logger.Info("Waiting for peer to be ready...", "error", err.Error())
				select {
				case <-ctx.Done():
					return
				case <-time.After(2 * time.Second):
				}
				continue
			}
			logger.Info("Peer is ready")
			break
		}

		// Main send loop
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Duration(intervalSecs)*time.Second + randomJitter()):
				sendPayload(client, sendURL, svcName, logger)
			}
		}
	}()

	// 3. Graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	<-sigCh
	logger.Info("Shutting down...")
	cancel()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	server.Shutdown(shutdownCtx)

	wg.Wait()
	logger.Info("Shutdown complete")
}

func pingPeer(client *http.Client, url string) error {
	// A simple GET to the ingest URL will return 405 Method Not Allowed if the server is up
	// and the mTLS handshake succeeds. If it fails, we get a transport/connection error.
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

func sendPayload(client *http.Client, url string, from string, logger *slog.Logger) {
	start := time.Now()
	payload, id := generatePayload(from)

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		logger.Error("failed to create request", "error", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		logger.Error("failed to send request", "error", err)
		return
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	peerCN := "unknown"
	if resp.TLS != nil && len(resp.TLS.PeerCertificates) > 0 {
		peerCN = resp.TLS.PeerCertificates[0].Subject.CommonName
	}

	logger.Info("Client sent payload",
		"peer_cn", peerCN,
		"msg_id", id,
		"bytes_out", len(payload),
		"bytes_in", len(respBody),
		"status", resp.StatusCode,
		"latency_ms", time.Since(start).Milliseconds(),
	)
}

func generatePayload(from string) ([]byte, string) {
	idxBig, _ := rand.Int(rand.Reader, big.NewInt(int64(len(payloadGenerators))))
	gen := payloadGenerators[idxBig.Int64()]

	idBytes := make([]byte, 8)
	rand.Read(idBytes)
	id := hex.EncodeToString(idBytes)

	reqBody, _ := gen(id)
	b, _ := json.Marshal(reqBody)
	return b, id
}

func randomJitter() time.Duration {
	n, _ := rand.Int(rand.Reader, big.NewInt(1000))
	return time.Duration(n.Int64()) * time.Millisecond
}
