FROM python:3.12-slim-bookworm

# match the Helm chart's podSecurityContext (runAsUser/runAsGroup: 65532)
RUN groupadd -g 65532 nonroot && useradd -u 65532 -g nonroot -M -s /usr/sbin/nologin nonroot

WORKDIR /app

COPY requirements.txt /app/requirements.txt

RUN pip install --no-cache-dir -r requirements.txt && \
    python3 -c "import urllib.request; urllib.request.urlretrieve('https://huggingface.co/beki/en_spacy_pii_fast/resolve/main/en_spacy_pii_fast-any-py3-none-any.whl', '/tmp/en_spacy_pii_fast-1.0.0-py3-none-any.whl')" && \
    python3 -c "import urllib.request; urllib.request.urlretrieve('https://huggingface.co/beki/en_spacy_pii_distilbert/resolve/main/en_spacy_pii_distilbert-any-py3-none-any.whl', '/tmp/en_spacy_pii_distilbert-1.0.0-py3-none-any.whl')" && \
    pip install --no-cache-dir --no-deps /tmp/en_spacy_pii_fast-1.0.0-py3-none-any.whl && \
    pip install --no-cache-dir --no-deps /tmp/en_spacy_pii_distilbert-1.0.0-py3-none-any.whl && \
    rm -f /tmp/*.whl

COPY --chown=65532:65532 app/ /app/app/
COPY --chown=65532:65532 entrypoint.sh /app/entrypoint.sh

RUN chmod +x /app/entrypoint.sh

USER 65532

EXPOSE 4317 5000
ENV PYTHONPATH=/app
ENTRYPOINT ["/app/entrypoint.sh"]