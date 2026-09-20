#!/usr/bin/env bash
set -e

# Ensure script runs from inside pixie_mock directory
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"
chmod +x "$0"

# Check if --docker flag was passed
if [ "$1" == "--docker" ]; then
    echo "=== [1/2] Downloading NLP Model Wheels (Fast & DistilBERT) ==="
    if [ ! -f "en_spacy_pii_fast.whl" ]; then
        echo "Downloading en_spacy_pii_fast.whl..."
        wget -O en_spacy_pii_fast.whl https://huggingface.co/beki/en_spacy_pii_fast/resolve/main/en_spacy_pii_fast-any-py3-none-any.whl
    fi
    if [ ! -f "en_spacy_pii_distilbert.whl" ]; then
        echo "Downloading en_spacy_pii_distilbert.whl..."
        wget -O en_spacy_pii_distilbert.whl https://huggingface.co/beki/en_spacy_pii_distilbert/resolve/main/en_spacy_pii_distilbert-any-py3-none-any.whl
    fi

    # Ensure wheels are named with valid PEP440 versions for pip
    cp en_spacy_pii_fast.whl en_spacy_pii_fast-1.0.0-py3-none-any.whl 2>/dev/null || true
    cp en_spacy_pii_distilbert.whl en_spacy_pii_distilbert-1.0.0-py3-none-any.whl 2>/dev/null || true

    echo "=== [2/2] Building and Running Docker Container ==="
    docker build -t pixie-mock:latest .
    docker run --rm -p 5000:5000 -p 4317:4317 --name pixie-mock-container pixie-mock:latest
    exit 0
fi

echo "=== [1/5] Downloading SpaCy PII Model Wheels ==="
if [ ! -f "en_spacy_pii_fast.whl" ]; then
    echo "Downloading Fast model (en_spacy_pii_fast.whl)..."
    wget -O en_spacy_pii_fast.whl https://huggingface.co/beki/en_spacy_pii_fast/resolve/main/en_spacy_pii_fast-any-py3-none-any.whl
fi

if [ ! -f "en_spacy_pii_distilbert.whl" ]; then
    echo "Downloading DistilBERT model (en_spacy_pii_distilbert.whl)..."
    wget -O en_spacy_pii_distilbert.whl https://huggingface.co/beki/en_spacy_pii_distilbert/resolve/main/en_spacy_pii_distilbert-any-py3-none-any.whl
fi

# Create valid PEP440 wheel filenames for pip installation
cp en_spacy_pii_fast.whl en_spacy_pii_fast-1.0.0-py3-none-any.whl 2>/dev/null || true
cp en_spacy_pii_distilbert.whl en_spacy_pii_distilbert-1.0.0-py3-none-any.whl 2>/dev/null || true

echo "=== [2/5] Setting up Virtual Environment ==="
if [ ! -d "venv" ]; then
    python3 -m venv venv
    echo "Created virtual environment 'venv'."
fi

source venv/bin/activate

echo "=== [3/5] Installing Dependencies from requirements.txt ==="
pip install --upgrade pip
pip install -r requirements.txt

echo "=== [4/5] Installing Both Local SpaCy PII Models ==="
pip install --no-deps en_spacy_pii_fast-1.0.0-py3-none-any.whl
pip install --no-deps en_spacy_pii_distilbert-1.0.0-py3-none-any.whl

echo "=== [5/5] Starting gRPC OTLP Receiver, Flask Web App & Mock Vizier ==="

cleanup() {
    echo ""
    echo "=== Shutting down background processes... ==="
    kill $(jobs -p) 2>/dev/null || true
    echo "Done."
}
trap cleanup EXIT INT TERM

# Start gRPC receiver on port 4317 in background
PYTHONPATH=. python3 app/otlp_receiver.py &
RECEIVER_PID=$!
echo "Started OTLP gRPC Receiver (PID: $RECEIVER_PID)"

# Start Flask Web App on port 5000 in background
PYTHONPATH=. python3 app/web_app.py &
WEB_PID=$!
echo "Started Flask Web App (PID: $WEB_PID)"

sleep 3

echo ""
echo "=========================================================="
echo "  Pixie Mock Dashboard running at: http://localhost:5000"
echo "  OTLP gRPC Receiver running at: localhost:4317"
echo "=========================================================="
echo ""

echo "=== Running Mock Vizier (OTLP Log Sender) ==="
PYTHONPATH=. python3 test_client/mock_vizier.py
