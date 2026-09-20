import concurrent.futures
from datetime import datetime
import json
import logging
import os
from typing import Any, Dict

import grpc
from opentelemetry.proto.collector.logs.v1 import logs_service_pb2, logs_service_pb2_grpc
import spacy

from app.state import state

logging.basicConfig(level=logging.INFO, format="%(asctime)s [%(levelname)s] %(name)s: %(message)s")
logger = logging.getLogger("otlp_receiver")

# Load BOTH Models on Startup
logger.info("Initializing SpaCy NLP models...")

MODEL_FAST_NAME = "en_spacy_pii_fast"
MODEL_DISTILBERT_NAME = "en_spacy_pii_distilbert"

try:
    logger.info(f"Loading Fast model '{MODEL_FAST_NAME}'...")
    model_fast = spacy.load(MODEL_FAST_NAME)
    logger.info("Fast model loaded successfully.")
except Exception as e:
    logger.warning(f"Could not load '{MODEL_FAST_NAME}': {e}. Using blank English fallback for Fast model.")
    model_fast = spacy.blank("en")

try:
    logger.info(f"Loading DistilBERT Transformer model '{MODEL_DISTILBERT_NAME}'...")
    model_distilbert = spacy.load(MODEL_DISTILBERT_NAME)
    logger.info("DistilBERT model loaded successfully.")
except Exception as e:
    logger.warning(f"Could not load '{MODEL_DISTILBERT_NAME}': {e}. Falling back to Fast model.")
    model_distilbert = None


def _extract_any_value(val) -> str:
    """Recursively extract string content from OpenTelemetry AnyValue protobuf."""
    if not val:
        return ""
    field = val.WhichOneof("value")
    if field == "string_value":
        return val.string_value
    elif field == "bytes_value":
        return val.bytes_value.decode("utf-8", errors="ignore")
    elif field == "int_value":
        return str(val.int_value)
    elif field == "double_value":
        return str(val.double_value)
    elif field == "bool_value":
        return str(val.bool_value)
    elif field == "kvlist_value":
        kv_dict = {kv.key: _extract_any_value(kv.value) for kv in val.kvlist_value.values}
        return json.dumps(kv_dict)
    elif field == "array_value":
        arr = [_extract_any_value(v) for v in val.array_value.values]
        return json.dumps(arr)
    return str(val)


def process_log_record(body_str: str, resource_attrs: Dict[str, str] = None):
    """Process log body through currently selected NLP model (fast or distilbert)."""
    current_model_choice = state.current_model_name

    # Select active model
    if current_model_choice == "distilbert" and model_distilbert is not None:
        active_nlp = model_distilbert
        active_model_used = "distilbert"
    else:
        active_nlp = model_fast
        active_model_used = "fast"

    # Run Inference
    doc = active_nlp(body_str)
    entities = []
    for ent in doc.ents:
        entities.append({
            "text": ent.text,
            "label": ent.label_,
            "start": ent.start_char,
            "end": ent.end_char
        })

    entry = {
        "id": len(state.get_logs()) + 1,
        "timestamp": datetime.utcnow().isoformat() + "Z",
        "log_body": body_str,
        "has_pii": len(entities) > 0,
        "entities": entities,
        "model_used": active_model_used,
        "resource_attributes": resource_attrs or {}
    }

    state.add_log(entry)
    logger.info(f"[{active_model_used.upper()} MODEL] Processed log (PII: {entry['has_pii']}, Entities: {len(entities)}): {body_str[:80]}")


class OTLPLogsServicer(logs_service_pb2_grpc.LogsServiceServicer):
    """Official OTLP LogsService gRPC Servicer implementation."""

    def Export(self, request: logs_service_pb2.ExportLogsServiceRequest, context) -> logs_service_pb2.ExportLogsServiceResponse:
        try:
            for resource_log in request.resource_logs:
                res_attrs = {}
                if resource_log.HasField("resource"):
                    for attr in resource_log.resource.attributes:
                        res_attrs[attr.key] = _extract_any_value(attr.value)

                scope_logs_list = getattr(resource_log, "scope_logs", None)
                if scope_logs_list is None:
                    scope_logs_list = getattr(resource_log, "instrumentation_library_logs", [])

                for scope_log in scope_logs_list:
                    for log_record in scope_log.log_records:
                        body_str = _extract_any_value(log_record.body)
                        if body_str:
                            process_log_record(body_str, res_attrs)

            return logs_service_pb2.ExportLogsServiceResponse()
        except Exception as e:
            logger.error(f"Error processing Export request: {e}", exc_info=True)
            context.set_code(grpc.StatusCode.INTERNAL)
            context.set_details(str(e))
            return logs_service_pb2.ExportLogsServiceResponse()


def serve_grpc(port: int = 4317):
    server = grpc.server(concurrent.futures.ThreadPoolExecutor(max_workers=10))
    logs_service_pb2_grpc.add_LogsServiceServicer_to_server(OTLPLogsServicer(), server)
    server.add_insecure_port(f"0.0.0.0:{port}")
    server.start()
    logger.info(f"OTLP gRPC Receiver running on port {port}...")
    return server


if __name__ == "__main__":
    server = serve_grpc(4317)
    try:
        server.wait_for_termination()
    except KeyboardInterrupt:
        logger.info("Stopping OTLP gRPC receiver...")
        server.stop(0)
