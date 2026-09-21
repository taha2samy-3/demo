import json
import logging
import random
import time

from opentelemetry._logs import LogRecord, SeverityNumber, set_logger_provider
from opentelemetry.exporter.otlp.proto.grpc._log_exporter import OTLPLogExporter
from opentelemetry.sdk._logs import LoggerProvider
from opentelemetry.sdk._logs.export import BatchLogRecordProcessor
from opentelemetry.sdk.resources import Resource

# Configure logging
logging.basicConfig(level=logging.INFO, format="%(asctime)s [%(levelname)s] %(name)s: %(message)s")
logger = logging.getLogger("mock_vizier")


def main():
    logger.info("Initializing OpenTelemetry OTLP Log Sender (Mock Vizier)...")

    resource = Resource.create({"service.name": "pixie-mock-vizier", "k8s.namespace.name": "demo"})
    logger_provider = LoggerProvider(resource=resource)
    set_logger_provider(logger_provider)

    exporter = OTLPLogExporter(endpoint="localhost:4317", insecure=True)
    logger_provider.add_log_record_processor(BatchLogRecordProcessor(exporter))

    otel_logger = logger_provider.get_logger("vizier-http-body-exporter")

    payloads = [
        # Safe payloads
        {"status": "ok", "service": "payment-gateway", "response_code": 200, "latency_ms": 14},
        {"action": "health_check", "status": "healthy", "uptime_sec": 3600},
        {"event": "item_searched", "query": "wireless headphones", "category": "electronics"},
        {"route": "/api/v1/metrics", "method": "GET", "status": 200},
        # PII Payloads (Simulating Pixie captured HTTP request/response bodies)
        {"user": "John Doe", "email": "john.doe@acme.com", "action": "update_profile"},
        {"customer": "Alice Smith", "address": "123 Main Street, New York", "order_id": 98412},
        {"contact": "Robert Johnson", "organization": "Microsoft", "phone": "+1-555-0199"},
        {"patient_name": "Emily Davis", "location": "Boston General Hospital", "note": "Scheduled consultation"},
        {"account_holder": "Michael Brown", "city": "San Francisco", "company": "Google"},
    ]

    logger.info("Starting OTLP Log transmission loop (Sending every 3 seconds to localhost:4317)...")

    count = 0
    try:
        while True:
            payload = random.choice(payloads)
            payload_copy = dict(payload)
            payload_copy["seq"] = count
            payload_str = json.dumps(payload_copy)

            log_record = LogRecord(
                timestamp=int(time.time() * 1e9),
                body=payload_str,
                severity_number=SeverityNumber.INFO,
                severity_text="INFO",
            )

            otel_logger.emit(log_record)
            logger.info(f"Sent OTLP log #{count}: {payload_str}")

            count += 1
            time.sleep(3)
    except KeyboardInterrupt:
        logger.info("Stopping mock vizier sender...")
    finally:
        logger_provider.shutdown()


if __name__ == "__main__":
    main()
