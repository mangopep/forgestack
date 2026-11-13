"""ForgeStack Python reference service entry point.

Graceful shutdown note: uvicorn already handles SIGINT/SIGTERM by stopping
the accept loop and waiting for in-flight requests before the process
exits -- unlike Go's net/http.Server, which needs an explicit
signal.NotifyContext + Server.Shutdown() call (see cmd/api/main.go). The
lifespan hook below only logs the transition; it does not need to
reimplement what the server already does.
"""

from __future__ import annotations

import logging
from collections.abc import AsyncIterator
from contextlib import asynccontextmanager

from fastapi import FastAPI

from app.config import get_settings
from app.errors import register_error_handlers
from app.logging_config import configure_logging, log_with_fields
from app.middleware import CorrelationIDMiddleware, MaxBodySizeMiddleware, RequestLoggingMiddleware
from app.routers import health, items
from app.store import InMemoryItemStore

logger = logging.getLogger("app.lifespan")


@asynccontextmanager
async def lifespan(app: FastAPI) -> AsyncIterator[None]:
    log_with_fields(
        logger, logging.INFO, "service starting", service=app.state.settings.service_name
    )
    yield
    log_with_fields(logger, logging.INFO, "service stopping")


def create_app() -> FastAPI:
    settings = get_settings()
    configure_logging(settings.log_level)

    app = FastAPI(
        title=settings.service_name,
        version=settings.service_version,
        lifespan=lifespan,
    )
    app.state.settings = settings
    app.state.store = InMemoryItemStore()

    # Order matters: Starlette applies middleware outer-to-inner in the
    # order added, so CorrelationID must run first (it sets the context
    # var RequestLoggingMiddleware's log lines then read).
    app.add_middleware(MaxBodySizeMiddleware, max_bytes=settings.request_body_max_bytes)
    app.add_middleware(RequestLoggingMiddleware)
    app.add_middleware(CorrelationIDMiddleware)

    register_error_handlers(app)

    app.include_router(health.router)
    app.include_router(items.router)

    return app


app = create_app()
