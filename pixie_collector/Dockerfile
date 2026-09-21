FROM python:3.12-slim-bookworm

WORKDIR /app

# Copy requirements
COPY requirements.txt /app/requirements.txt

# Install Python packages and both SpaCy PII models
RUN pip install --no-cache-dir -r requirements.txt && \
    python3 -c "import urllib.request; urllib.request.urlretrieve('https://huggingface.co/beki/en_spacy_pii_fast/resolve/main/en_spacy_pii_fast-any-py3-none-any.whl', '/tmp/en_spacy_pii_fast-1.0.0-py3-none-any.whl')" && \
    python3 -c "import urllib.request; urllib.request.urlretrieve('https://huggingface.co/beki/en_spacy_pii_distilbert/resolve/main/en_spacy_pii_distilbert-any-py3-none-any.whl', '/tmp/en_spacy_pii_distilbert-1.0.0-py3-none-any.whl')" && \
    pip install --no-cache-dir --no-deps /tmp/en_spacy_pii_fast-1.0.0-py3-none-any.whl && \
    pip install --no-cache-dir --no-deps /tmp/en_spacy_pii_distilbert-1.0.0-py3-none-any.whl && \
    rm -f /tmp/*.whl


# Copy application source code
COPY app/ /app/app/
COPY entrypoint.sh /app/entrypoint.sh

RUN chmod +x /app/entrypoint.sh

EXPOSE 4317 5000

ENV PYTHONPATH=/app

ENTRYPOINT ["/app/entrypoint.sh"]


