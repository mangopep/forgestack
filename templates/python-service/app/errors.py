"""Exception handlers producing the same {"error": "<message>"} response
shape the Go API uses (internal/platform/apierror + transport.encodeError)
-- see docs/platform/CONTRACTS.md, "HTTP errors" section, including the
one documented, deliberate difference: FastAPI's native 422 for request
validation errors is kept (not forced into Go's 400), since 422 is the
idiomatic, more specific status for "syntactically valid but semantically
invalid" in this framework -- the response *body* shape is what's
contractually shared, not every status code.
"""

from __future__ import annotations

import logging

from fastapi import FastAPI, Request
from fastapi.exceptions import RequestValidationError
from fastapi.responses import JSONResponse
from starlette.exceptions import HTTPException as StarletteHTTPException

logger = logging.getLogger("app.errors")


def register_error_handlers(app: FastAPI) -> None:
    @app.exception_handler(StarletteHTTPException)
    async def handle_http_exception(request: Request, exc: StarletteHTTPException) -> JSONResponse:
        return JSONResponse(status_code=exc.status_code, content={"error": str(exc.detail)})

    @app.exception_handler(RequestValidationError)
    async def handle_validation_error(
        request: Request, exc: RequestValidationError
    ) -> JSONResponse:
        # exc.errors() can contain the raw submitted value, which may be
        # sensitive -- only the field path and a generic reason are
        # returned to the client, never echoed back verbatim.
        fields = ", ".join(".".join(str(p) for p in e["loc"][1:]) for e in exc.errors())
        return JSONResponse(status_code=422, content={"error": f"invalid request: {fields}"})

    @app.exception_handler(Exception)
    async def handle_unhandled_exception(request: Request, exc: Exception) -> JSONResponse:
        # Never leak an unclassified exception's message to the client --
        # same rule as the Go API's apierror package. Full detail goes to
        # the server log only.
        logger.exception("unhandled exception")
        return JSONResponse(status_code=500, content={"error": "internal error"})
