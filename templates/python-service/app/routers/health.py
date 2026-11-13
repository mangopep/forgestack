from __future__ import annotations

from fastapi import APIRouter, Request

router = APIRouter()


@router.get("/healthz")
async def healthz(request: Request) -> dict[str, str]:
    settings = request.app.state.settings
    return {"status": "ok", "service": settings.service_name, "version": settings.service_version}


@router.get("/readyz")
async def readyz() -> dict[str, str]:
    return {"status": "ready"}
