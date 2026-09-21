#!/usr/bin/env bash
set -e

cleanup() {
    echo "Shutting down container services..."
    kill $(jobs -p) 2>/dev/null || true
    exit 0
}
trap cleanup EXIT INT TERM

echo "=== [1/2] Starting OTLP gRPC Receiver on port 4317 ==="
python3 app/otlp_receiver.py &
RECEIVER_PID=$!

echo "=== [2/2] Starting Flask Dashboard on port 5000 ==="
python3 app/web_app.py &
WEB_PID=$!

sleep 3

echo ""
echo "=========================================================="
echo "  Pixie Collector Container Started!"
echo "  Dashboard: http://localhost:5000"
echo "  OTLP gRPC Receiver: localhost:4317"
echo "=========================================================="
echo ""

echo "=== Listening for OTLP exports on 0.0.0.0:4317 ==="
wait -n $RECEIVER_PID $WEB_PID


