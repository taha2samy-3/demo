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
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

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
	listenAddr := os.Getenv("LISTEN_ADDR")
	if listenAddr == "" {
		listenAddr = ":8443"
	}
	intervalSecs := 5
	if v := os.Getenv("SEND_INTERVAL_SECONDS"); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			intervalSecs = i
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

		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "Bad request", http.StatusBadRequest)
			return
		}

		peerCN := "unknown"
		if r.TLS != nil && len(r.TLS.PeerCertificates) > 0 {
			peerCN = r.TLS.PeerCertificates[0].Subject.CommonName
		}

		// parse partial id for logging only
		var reqData struct {
			ID string `json:"id"`
		}
		json.Unmarshal(body, &reqData)

		respData := map[string]string{"status": "ok", "msg": "received"}
		respBytes, _ := json.Marshal(respData)
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
	}

	// 2. Client setup
	peerHost := strings.Split(peerAddr, ":")[0]
	clientTLSConfig := &tls.Config{
		RootCAs:      caPool,
		Certificates: []tls.Certificate{cert},
		ServerName:   peerHost,
		MinVersion:   tls.VersionTLS12,
		NextProtos:   []string{"http/1.1"}, // Disable HTTP/2 negotiation
	}

	tr := &http.Transport{
		TLSClientConfig:   clientTLSConfig,
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
				time.Sleep(2 * time.Second)
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
	// Payload between ~200 bytes and ~2 KB
	size, _ := rand.Int(rand.Reader, big.NewInt(1800))
	padSize := 100 + size.Int64()
	pad := make([]byte, padSize)
	rand.Read(pad)

	secret := make([]byte, 16)
	rand.Read(secret)
	secretHex := hex.EncodeToString(secret)
	
	idBytes := make([]byte, 8)
	rand.Read(idBytes)
	id := hex.EncodeToString(idBytes)

	data := map[string]interface{}{
		"id":        id,
		"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
		"from":      from,
		"marker":    "DEMO-SECRET-" + secretHex,
		"padding":   hex.EncodeToString(pad), // doubles the byte size of padding
		"words":     "lorem ipsum dolor sit amet",
	}
	b, _ := json.Marshal(data)
	return b, id
}

func randomJitter() time.Duration {
	n, _ := rand.Int(rand.Reader, big.NewInt(1000))
	return time.Duration(n.Int64()) * time.Millisecond
}
