package main

import (
	"context"
	"encoding/json"
	"log"
	"math/rand"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
	"go.opentelemetry.io/otel/trace"
)

type SpanPayload struct {
	Name           string
	Method         string
	StatusCode     int64
	ReqBody        string
	RespBody       string
	PeerIP         string
	PodName        string
}

func main() {
	log.Println("Starting Pixie Vizier Go OTLP Trace Generator...")

	// 1. Parse Environment Configuration
	endpoint := getEnv("COLLECTOR_ENDPOINT", "localhost:4317")
	insecureStr := getEnv("INSECURE", "true")
	intervalMsStr := getEnv("SEND_INTERVAL_MS", "3000")

	intervalMs, err := strconv.Atoi(intervalMsStr)
	if err != nil || intervalMs < 100 {
		intervalMs = 3000
	}

	isInsecure := insecureStr == "true" || insecureStr == "1"

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 2. Configure OTLP gRPC Trace Exporter
	exporterOpts := []otlptracegrpc.Option{
		otlptracegrpc.WithEndpoint(endpoint),
	}
	if isInsecure {
		exporterOpts = append(exporterOpts, otlptracegrpc.WithInsecure())
	}

	exporter, err := otlptracegrpc.New(ctx, exporterOpts...)
	if err != nil {
		log.Fatalf("Failed to create OTLP trace exporter: %v", err)
	}

	// 3. Define Resource Attributes matching Pixie Vizier telemetry
	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceNameKey.String("pixie-mock-vizier-go"),
			attribute.String("k8s.namespace.name", "demo"),
		),
	)
	if err != nil {
		log.Fatalf("Failed to create OTel resource: %v", err)
	}

	// 4. Initialize TracerProvider with BatchSpanProcessor
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)

	tracer := tp.Tracer("vizier-http-span-exporter")

	log.Printf("Connected OTLP gRPC Trace Exporter to %s (Insecure: %v, Interval: %dms)", endpoint, isInsecure, intervalMs)

	// 5. Setup Graceful Shutdown Signal Handler
	stopSignal := make(chan os.Signal, 1)
	signal.Notify(stopSignal, os.Interrupt, syscall.SIGTERM)

	// 6. Predefined Span Rehearsal Payloads (Clean + PII Rich)
	payloads := getSamplePayloads()

	ticker := time.NewTicker(time.Duration(intervalMs) * time.Millisecond)
	defer ticker.Stop()

	seq := 0
	log.Println("Starting trace span transmission loop...")

	for {
		select {
		case <-stopSignal:
			log.Println("Received termination signal. Flushing and shutting down TracerProvider...")
			shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer shutdownCancel()
			if err := tp.Shutdown(shutdownCtx); err != nil {
				log.Printf("Error shutting down TracerProvider: %v", err)
			} else {
				log.Println("TracerProvider cleanly shut down.")
			}
			return

		case <-ticker.C:
			seq++
			sample := payloads[rand.Intn(len(payloads))]

			// Inject sequence numbers for easy tracking
			reqBody := sample.ReqBody
			respBody := sample.RespBody

			// Create Span
			_, span := tracer.Start(ctx, sample.Name,
				trace.WithSpanKind(trace.SpanKindServer),
				trace.WithAttributes(
					attribute.String("http.method", sample.Method),
					attribute.Int64("http.status_code", sample.StatusCode),
					attribute.String("net.peer.ip", sample.PeerIP),
					attribute.String("k8s.pod.name", sample.PodName),
					attribute.String("req_body", reqBody),
					attribute.String("resp_body", respBody),
					attribute.Int("seq", seq),
				),
			)
			span.End()

			log.Printf("[SPAN #%d] Sent trace '%s %s' (Pod: %s, ReqBody len: %d, RespBody len: %d)",
				seq, sample.Method, sample.Name, sample.PodName, len(reqBody), len(respBody))
		}
	}
}

func getEnv(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}

func getSamplePayloads() []SpanPayload {
	return []SpanPayload{
		// Clean / Benign Payloads
		{
			Name:       "/healthz",
			Method:     "GET",
			StatusCode: 200,
			ReqBody:    "",
			RespBody:   `{"status":"healthy","uptime_sec":86400,"service":"frontend"}`,
			PeerIP:     "10.244.0.12",
			PodName:    "frontend-6b4594c77f-m2k9r",
		},
		{
			Name:       "/api/v1/metrics",
			Method:     "GET",
			StatusCode: 200,
			ReqBody:    "",
			RespBody:   `{"cpu_utilization":14.2,"memory_mb":512,"active_connections":89}`,
			PeerIP:     "10.244.1.33",
			PodName:    "checkoutservice-7b587d6d5-x8q2z",
		},
		{
			Name:       "/api/v1/products/search",
			Method:     "POST",
			StatusCode: 200,
			ReqBody:    `{"query":"wireless noise canceling headphones","category":"electronics","max_price":200}`,
			RespBody:   `{"items_found":14,"page":1,"total_pages":2}`,
			PeerIP:     "192.168.1.105",
			PodName:    "productcatalogservice-5975d4b585-k7p4w",
		},
		{
			Name:       "/api/v1/cart/item",
			Method:     "PUT",
			StatusCode: 200,
			ReqBody:    `{"item_id":"item_9941","quantity":2,"currency":"USD"}`,
			RespBody:   `{"cart_total_items":5,"subtotal":89.98}`,
			PeerIP:     "10.244.2.8",
			PodName:    "cartservice-6d8778f679-b9x2p",
		},
		// PII: Full Names, Email, Phone Numbers
		{
			Name:       "/api/v1/users/register",
			Method:     "POST",
			StatusCode: 201,
			ReqBody:    mustJSON(map[string]interface{}{"username": "johndoe", "email": "john.doe@acme-corp.com", "full_name": "John Doe", "phone": "+1-555-0199"}),
			RespBody:   `{"status":"created","user_id":"usr_99812"}`,
			PeerIP:     "10.244.0.15",
			PodName:    "userservice-84b6f79d9-l4q8v",
		},
		{
			Name:       "/api/v1/customer/contact-update",
			Method:     "PUT",
			StatusCode: 200,
			ReqBody:    mustJSON(map[string]interface{}{"contact_person": "Robert Johnson", "company": "Microsoft", "email": "rjohnson@microsoft.com", "phone": "+44 20 7946 0912"}),
			RespBody:   `{"updated":true,"timestamp":"2026-09-20T19:00:00Z"}`,
			PeerIP:     "192.168.1.44",
			PodName:    "customerservice-5d9859f77-x2p1m",
		},
		// PII: Credit Card & Physical Address
		{
			Name:       "/api/v1/checkout/payment",
			Method:     "POST",
			StatusCode: 200,
			ReqBody:    mustJSON(map[string]interface{}{"customer_name": "Sarah Connor", "card_number": "4532-1189-9021-4412", "billing_address": "742 Evergreen Terrace, Springfield, OR 97477", "amount_usd": 149.99}),
			RespBody:   `{"transaction_id":"tx_8831920","status":"approved"}`,
			PeerIP:     "10.244.1.99",
			PodName:    "paymentservice-58849b84b-p4l9k",
		},
		// PII: SSN, Tax ID & Full Address
		{
			Name:       "/api/v1/identity/verify-ssn",
			Method:     "POST",
			StatusCode: 200,
			ReqBody:    mustJSON(map[string]interface{}{"applicant_name": "Alexander Hamilton", "ssn": "123-45-6789", "tax_id": "987-65-4321", "address": "1600 Pennsylvania Ave NW, Washington, DC"}),
			RespBody:   `{"verification_status":"verified","credit_score":790}`,
			PeerIP:     "10.0.4.12",
			PodName:    "identityservice-7c89f5d44-z8n3q",
		},
		// PII: Health / Medical Records Context
		{
			Name:       "/api/v1/patients/medical-record",
			Method:     "GET",
			StatusCode: 200,
			ReqBody:    `{"patient_id":"p_4412","doctor_id":"doc_12"}`,
			RespBody:   mustJSON(map[string]interface{}{"patient_name": "Emily Davis", "hospital": "Boston General Hospital", "diagnosis": "Acute Bronchitis", "note": "Prescribed Amoxicillin 500mg, review in 7 days"}),
			PeerIP:     "10.244.3.18",
			PodName:    "ehr-service-67f9c8b86-m9p2x",
		},
		// PII: Support Ticket with Customer Details
		{
			Name:       "/api/v1/support/ticket",
			Method:     "POST",
			StatusCode: 201,
			ReqBody:    mustJSON(map[string]interface{}{"submitted_by": "Michael Brown", "company": "Google", "phone": "555-867-5309", "city": "San Francisco", "issue": "Billing discrepancy on invoice #1002"}),
			RespBody:   `{"ticket_id":"TICK-4419","assigned_group":"support-tier2"}`,
			PeerIP:     "192.168.2.110",
			PodName:    "supportservice-568b997c4-w7n1v",
		},
	}
}

func mustJSON(v interface{}) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}
