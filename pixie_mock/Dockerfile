FROM python:3.12-slim

# Install system dependencies
RUN apt-get update && apt-get install -y --no-install-recommends \
    curl \
    gcc \
    git \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

# Copy requirements and model wheels
COPY requirements.txt /app/requirements.txt
COPY en_spacy_pii_fast-1.0.0-py3-none-any.whl /app/en_spacy_pii_fast-1.0.0-py3-none-any.whl
COPY en_spacy_pii_distilbert-1.0.0-py3-none-any.whl /app/en_spacy_pii_distilbert-1.0.0-py3-none-any.whl

# Install Python packages and both SpaCy PII models
RUN pip install --no-cache-dir -r requirements.txt && \
    pip install --no-cache-dir --no-deps /app/en_spacy_pii_fast-1.0.0-py3-none-any.whl && \
    pip install --no-cache-dir --no-deps /app/en_spacy_pii_distilbert-1.0.0-py3-none-any.whl

# Copy application source code
COPY app/ /app/app/
COPY test_client/ /app/test_client/
COPY entrypoint.sh /app/entrypoint.sh

RUN chmod +x /app/entrypoint.sh

EXPOSE 4317 5000

ENV PYTHONPATH=/app

ENTRYPOINT ["/app/entrypoint.sh"]
