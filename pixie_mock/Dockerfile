FROM python:3.12-slim-bookworm

WORKDIR /app

# Copy requirements
COPY requirements.txt /app/requirements.txt

# Install Python packages and both SpaCy PII models from Hugging Face
RUN pip install --no-cache-dir -r requirements.txt && \
    pip install --no-cache-dir --no-deps https://huggingface.co/beki/en_spacy_pii_fast/resolve/main/en_spacy_pii_fast-any-py3-none-any.whl && \
    pip install --no-cache-dir --no-deps https://huggingface.co/beki/en_spacy_pii_distilbert/resolve/main/en_spacy_pii_distilbert-any-py3-none-any.whl

# Copy application source code
COPY app/ /app/app/
COPY test_client/ /app/test_client/
COPY entrypoint.sh /app/entrypoint.sh

RUN chmod +x /app/entrypoint.sh

EXPOSE 4317 5000

ENV PYTHONPATH=/app
ENV ENABLE_MOCK_VIZIER=false

ENTRYPOINT ["/app/entrypoint.sh"]

