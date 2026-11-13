"""Correlation ID and request-logging middleware. The header name and
reuse-or-generate behavior exactly mirror the Go API's
internal/platform/middleware.CorrelationID -- see
docs/platform/CONTRACTS.md for the cross-language contract this
implements independently in each language, not via a shared library.
"""

from __future__ import annotations

import logging
import secrets
import time

from starlette.middleware.base import BaseHTTPMiddleware, RequestResponseEndpoint
from starlette.requests import Request
from starlette.responses import Response

from app.logging_config import log_with_fields, request_id_ctx

REQUEST_ID_HEADER = "X-Request-Id"

logger = logging.getLogger("app.request")


class CorrelationIDMiddleware(BaseHTTPMiddleware):
    async def dispatch(self, request: Request, call_next: RequestResponseEndpoint) -> Response:
        request_id = request.headers.get(REQUEST_ID_HEADER) or secrets.token_hex(16)
        token = request_id_ctx.set(request_id)
        try:
            response = await call_next(request)
        finally:
            request_id_ctx.reset(token)
        response.headers[REQUEST_ID_HEADER] = request_id
        return response


class RequestLoggingMiddleware(BaseHTTPMiddleware):
    async def dispatch(self, request: Request, call_next: RequestResponseEndpoint) -> Response:
        start = time.monotonic()
        response = await call_next(request)
        duration_ms = int((time.monotonic() - start) * 1000)
        log_with_fields(
            logger,
            logging.INFO,
            "http_request",
            method=request.method,
            path=request.url.path,
            status=response.status_code,
            duration_ms=duration_ms,
        )
        return response


class MaxBodySizeMiddleware(BaseHTTPMiddleware):
    """Mirrors the Go API's middleware.MaxBytes: rejects a request whose
    declared Content-Length exceeds the configured limit before the body
    is read. This is a Content-Length check, not a streaming byte-counter
    -- a client that lies about Content-Length and streams more than
    declared is a separate, framework-level concern (Starlette/uvicorn
    already bound total request size via their own server-level limits).
    """

    def __init__(self, app: object, max_bytes: int) -> None:
        super().__init__(app)  # type: ignore[arg-type]
        self.max_bytes = max_bytes

    async def dispatch(self, request: Request, call_next: RequestResponseEndpoint) -> Response:
        content_length = request.headers.get("content-length")
        if content_length is not None and int(content_length) > self.max_bytes:
            from starlette.responses import JSONResponse

            return JSONResponse(
                status_code=413,
                content={"error": f"request body exceeds {self.max_bytes} byte limit"},
            )
        return await call_next(request)
