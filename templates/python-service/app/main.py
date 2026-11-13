"""__SERVICE_NAME__ entry point, generated from a ForgeStack Python service
template. This file does not import anything from ForgeStack's own
repository -- it is a complete, standalone starting point.

Graceful shutdown note: uvicorn already handles SIGINT/SIGTERM by stopping
the accept loop and waiting for in-flight requests before the process
exits.
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
from app.routers import health

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

    # Order matters: Starlette applies middleware outer-to-inner in the
    # order added, so CorrelationID must run first (it sets the context
    # var RequestLoggingMiddleware's log lines then read).
    app.add_middleware(MaxBodySizeMiddleware, max_bytes=settings.request_body_max_bytes)
    app.add_middleware(RequestLoggingMiddleware)
    app.add_middleware(CorrelationIDMiddleware)

    register_error_handlers(app)

    app.include_router(health.router)
    # Add your own routers here, e.g.:
    # app.include_router(widgets.router)

    return app


app = create_app()
