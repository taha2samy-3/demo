import json
import logging
import os
import threading
from typing import Any, Dict, List

logging.basicConfig(level=logging.INFO, format="%(asctime)s [%(levelname)s] %(name)s: %(message)s")
logger = logging.getLogger("state")

DATA_FILE = os.path.join(os.path.dirname(os.path.abspath(__file__)), "logs_data.json")


class AppState:
    """Thread-safe global state manager for A/B testing logs and active NLP model."""

    def __init__(self):
        self._lock = threading.Lock()
        self._logs: List[Dict[str, Any]] = []
        self._current_model_name: str = "fast"  # "fast" or "distilbert"
        self._next_id: int = 1

    @property
    def current_model_name(self) -> str:
        with self._lock:
            return self._current_model_name

    @current_model_name.setter
    def current_model_name(self, name: str):
        with self._lock:
            if name in ("fast", "distilbert"):
                self._current_model_name = name
                logger.info(f"Active NLP model switched to: '{name}'")
            else:
                logger.warning(f"Invalid model name requested: '{name}'")

    def add_log(self, entry: Dict[str, Any]) -> Dict[str, Any]:
        with self._lock:
            entry["id"] = self._next_id
            self._next_id += 1
            self._logs.append(entry)
            if len(self._logs) > 500:
                self._logs = self._logs[-500:]
            self._save_to_disk(entry)
            return entry

    def clear_logs(self):
        with self._lock:
            self._logs = []
            self._next_id = 1
            try:
                with open(DATA_FILE, "w", encoding="utf-8") as f:
                    json.dump([], f)
            except Exception as e:
                logger.error(f"Error clearing persisted logs: {e}")
            logger.info("Cleared all log/span records.")

    def get_logs(self) -> List[Dict[str, Any]]:
        with self._lock:
            if self._logs:
                return list(reversed(self._logs))
            if os.path.exists(DATA_FILE):
                try:
                    with open(DATA_FILE, "r", encoding="utf-8") as f:
                        data = json.load(f)
                        return list(reversed(data))
                except Exception:
                    pass
            return []

    def _save_to_disk(self, entry: Dict[str, Any]):
        try:
            data = []
            if os.path.exists(DATA_FILE):
                try:
                    with open(DATA_FILE, "r", encoding="utf-8") as f:
                        data = json.load(f)
                except Exception:
                    data = []
            data.append(entry)
            if len(data) > 500:
                data = data[-500:]
            with open(DATA_FILE, "w", encoding="utf-8") as f:
                json.dump(data, f, indent=2)
        except Exception as e:
            logger.error(f"Error persisting log entry: {e}")


# Global Singleton Instance
state = AppState()
