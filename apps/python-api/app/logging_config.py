"""Structured JSON logging -- deliberately stdlib-only (no structlog/
python-json-logger dependency) since a ~20-line Formatter covers exactly
what this service needs: one JSON object per log line, with the
correlation ID attached when a request is in flight.
"""

from __future__ import annotations

import json
import logging
import sys
import time
from contextvars import ContextVar

request_id_ctx: ContextVar[str] = ContextVar("request_id", default="")


class JSONFormatter(logging.Formatter):
    def format(self, record: logging.LogRecord) -> str:
        payload: dict[str, object] = {
            "timestamp": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime(record.created)),
            "level": record.levelname,
            "message": record.getMessage(),
            "logger": record.name,
        }
        request_id = request_id_ctx.get()
        if request_id:
            payload["request_id"] = request_id
        for key, value in getattr(record, "extra_fields", {}).items():
            payload[key] = value
        return json.dumps(payload)


def configure_logging(level: str) -> None:
    handler = logging.StreamHandler(sys.stdout)
    handler.setFormatter(JSONFormatter())
    root = logging.getLogger()
    root.handlers = [handler]
    root.setLevel(level.upper())


def log_with_fields(logger: logging.Logger, level: int, message: str, **fields: object) -> None:
    logger.log(level, message, extra={"extra_fields": fields})
