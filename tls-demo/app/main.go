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
	"strings"
	"sync"
	"syscall"
	"text/template"
	"time"

	"gopkg.in/yaml.v3"
)

// logger is set once in main() and read by code that runs outside main's
// own scope (the seed loader, and generatePayload/renderResponse which run
// on the client/server goroutines).
var logger *slog.Logger

// genericAck is the flat, no-information response used when a request
// carries no recognized "kind" (or seeds.yaml has no matching template).
var genericAck = []byte(`{"status":"ok","msg":"received"}`)

// SeedTemplate is one (request, response) shape as declared in seeds.yaml.
// Both fields are Go text/template source rendered against a context built
// by buildContext().
type SeedTemplate struct {
	Kind     string `yaml:"kind"`
	Request  string `yaml:"request"`
	Response string `yaml:"response"`
}

// Seeds is the full shape of seeds.yaml: a set of value pools that
// buildContext() draws random entries from, plus the request/response
// templates that reference them.
type Seeds struct {
	Pools     map[string][]string `yaml:"pools"`
	Templates []SeedTemplate      `yaml:"templates"`
}

// parsedTemplate holds a SeedTemplate's request/response bodies pre-parsed
// as text/template.Template so generatePayload() and renderResponse() only
// need to Execute(), not re-parse, on every call.
type parsedTemplate struct {
	kind     string
	request  *template.Template
	response *template.Template
}

var (
	seeds                 Seeds
	parsedTemplates       []parsedTemplate
	parsedTemplatesByKind map[string]*parsedTemplate
)

// loadSeeds reads and parses seeds.yaml. Growing traffic variety later is a
// pure data edit to that file (append a pools entry or a templates block) —
// this loader and generatePayload()/renderResponse() never need to change.
// Any problem with the file (missing, bad YAML, bad template syntax) is
// treated as a fatal startup error: the seeds ship in the image, so a
// broken file means a broken build, not a runtime condition to tolerate.
func loadSeeds(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		logger.Error("failed to read seeds file", "path", path, "error", err)
		os.Exit(1)
	}

	if err := yaml.Unmarshal(data, &seeds); err != nil {
		logger.Error("failed to parse seeds YAML", "path", path, "error", err)
		os.Exit(1)
	}
	if len(seeds.Templates) == 0 {
		logger.Error("seeds file has no templates", "path", path)
		os.Exit(1)
	}

	parsedTemplates = make([]parsedTemplate, 0, len(seeds.Templates))
	parsedTemplatesByKind = make(map[string]*parsedTemplate, len(seeds.Templates))
	for _, t := range seeds.Templates {
		reqTmpl, err := template.New(t.Kind + "_request").Parse(t.Request)
		if err != nil {
			logger.Error("failed to parse request template", "kind", t.Kind, "error", err)
			os.Exit(1)
		}
		respTmpl, err := template.New(t.Kind + "_response").Parse(t.Response)
		if err != nil {
			logger.Error("failed to parse response template", "kind", t.Kind, "error", err)
			os.Exit(1)
		}

		parsedTemplates = append(parsedTemplates, parsedTemplate{
			kind:     t.Kind,
			request:  reqTmpl,
			response: respTmpl,
		})
		parsedTemplatesByKind[t.Kind] = &parsedTemplates[len(parsedTemplates)-1]
	}

	logger.Info("loaded seed templates", "path", path, "count", len(parsedTemplates))
}

// randIndex returns a crypto/rand index in [0, n).
func randIndex(n int) int {
	idxBig, _ := rand.Int(rand.Reader, big.NewInt(int64(n)))
	return int(idxBig.Int64())
}

// pickRandom returns a random element from pool, or "" if it's empty.
func pickRandom(pool []string) string {
	if len(pool) == 0 {
		return ""
	}
	return pool[randIndex(len(pool))]
}

// randomHexID generates a random hex message id, the same way this file
// always has.
func randomHexID() string {
	idBytes := make([]byte, 8)
	rand.Read(idBytes)
	return hex.EncodeToString(idBytes)
}

// buildContext produces a fresh, fully-populated set of random field values.
// Every field is filled on every call regardless of which template will
// consume it: it's cheap, and it means any template can reference any field
// without extra wiring here.
func buildContext() map[string]string {
	firstName := pickRandom(seeds.Pools["first_names"])
	lastName := pickRandom(seeds.Pools["last_names"])
	domain := pickRandom(seeds.Pools["email_domains"])
	email := strings.ToLower(firstName+"."+lastName) + "@" + domain

	return map[string]string{
		"ID":               randomHexID(),
		"FirstName":        firstName,
		"LastName":         lastName,
		"Email":            email,
		"Phone":            pickRandom(seeds.Pools["phone_numbers"]),
		"Company":          pickRandom(seeds.Pools["companies"]),
		"JobTitle":         pickRandom(seeds.Pools["job_titles"]),
		"City":             pickRandom(seeds.Pools["cities"]),
		"Street":           pickRandom(seeds.Pools["streets"]),
		"Diagnosis":        pickRandom(seeds.Pools["diagnoses"]),
		"Medication":       pickRandom(seeds.Pools["medications"]),
		"Product":          pickRandom(seeds.Pools["products"]),
		"CardNumber":       pickRandom(seeds.Pools["test_card_numbers"]),
		"SSN":              pickRandom(seeds.Pools["fake_ssns"]),
		"OrderID":          pickRandom(seeds.Pools["order_ids"]),
		"TicketID":         pickRandom(seeds.Pools["ticket_ids"]),
		"BankName":         pickRandom(seeds.Pools["bank_names"]),
		"IBAN":             pickRandom(seeds.Pools["fake_ibans"]),
		"DeviceType":       pickRandom(seeds.Pools["device_types"]),
		"IP":               pickRandom(seeds.Pools["fake_ips"]),
		"EmployeeID":       pickRandom(seeds.Pools["employee_ids"]),
		"StudentID":        pickRandom(seeds.Pools["student_ids"]),
		"School":           pickRandom(seeds.Pools["schools"]),
		"PolicyNumber":     pickRandom(seeds.Pools["policy_numbers"]),
		"LoanAmount":       pickRandom(seeds.Pools["loan_amounts"]),
		"AppointmentType":  pickRandom(seeds.Pools["appointment_types"]),
		"SubscriptionPlan": pickRandom(seeds.Pools["subscription_plans"]),
	}
}

// renderResponse renders the response template for kind against a fresh
// context (the response doesn't need to echo the client's exact values,
// just be shape-appropriate). Falls back to genericAck if kind is missing,
// unrecognized, or fails to render/validate as JSON.
func renderResponse(kind string) []byte {
	pt, ok := parsedTemplatesByKind[kind]
	if !ok {
		return genericAck
	}

	var buf bytes.Buffer
	if err := pt.response.Execute(&buf, buildContext()); err != nil {
		logger.Warn("failed to render response template, using generic ack", "kind", kind, "error", err)
		return genericAck
	}
	rendered := buf.Bytes()
	if !json.Valid(rendered) {
		logger.Warn("rendered response template is not valid JSON, using generic ack", "kind", kind)
		return genericAck
	}
	return rendered
}

func main() {
	svcName := os.Getenv("SERVICE_NAME")
	if svcName == "" {
		svcName = "service-unknown"
	}
	logger = slog.New(slog.NewTextHandler(os.Stdout, nil)).With("service", svcName)

	loadSeeds("seeds.yaml")

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

		// Regenerate a shape-appropriate response from seeds.yaml rather
		// than trying to pass state across the network.
		respBytes := renderResponse(reqData.Kind)
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
	const maxAttempts = 20
	for attempt := 0; attempt < maxAttempts; attempt++ {
		pt := parsedTemplates[randIndex(len(parsedTemplates))]
		ctx := buildContext()

		var buf bytes.Buffer
		if err := pt.request.Execute(&buf, ctx); err != nil {
			logger.Warn("failed to render request template, skipping this attempt", "kind", pt.kind, "error", err)
			continue
		}
		rendered := buf.Bytes()
		if !json.Valid(rendered) {
			logger.Warn("rendered request template is not valid JSON, skipping this attempt", "kind", pt.kind)
			continue
		}
		return rendered, ctx["ID"]
	}

	logger.Error("no seed template produced valid JSON after repeated attempts; sending fallback payload")
	id := randomHexID()
	fallback, _ := json.Marshal(map[string]string{"id": id, "kind": "fallback"})
	return fallback, id
}

func randomJitter() time.Duration {
	n, _ := rand.Int(rand.Reader, big.NewInt(1000))
	return time.Duration(n.Int64()) * time.Millisecond
}
