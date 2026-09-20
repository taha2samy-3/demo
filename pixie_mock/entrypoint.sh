#!/usr/bin/env bash
set -e

cleanup() {
    echo "Shutting down container services..."
    kill $(jobs -p) 2>/dev/null || true
    exit 0
}
trap cleanup EXIT INT TERM

echo "=== [1/3] Starting OTLP gRPC Receiver on port 4317 ==="
python3 app/otlp_receiver.py &

echo "=== [2/3] Starting Flask Dashboard on port 5000 ==="
python3 app/web_app.py &

sleep 3

echo ""
echo "=========================================================="
echo "  Pixie Mock Container Started!"
echo "  Dashboard: http://localhost:5000"
echo "  OTLP gRPC Receiver: localhost:4317"
echo "=========================================================="
echo ""

echo "=== [3/3] Starting Mock Vizier OTLP Log Sender ==="
python3 test_client/mock_vizier.py
