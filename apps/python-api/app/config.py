"""Environment-validated configuration -- the Python half of ForgeStack's
service-metadata/configuration contract (docs/platform/CONTRACTS.md). Every
field is validated at startup, not read ad hoc from os.environ at call
sites, so a misconfigured deployment fails fast and loud instead of
surfacing as a confusing runtime error later.
"""

from __future__ import annotations

from pydantic import Field
from pydantic_settings import BaseSettings, SettingsConfigDict


class Settings(BaseSettings):
    model_config = SettingsConfigDict(env_prefix="", extra="ignore")

    service_name: str = Field(default="forgestack-python-api")
    # "dev" is the honest default for an unversioned local run -- never a
    # fabricated version number. Real builds set SERVICE_VERSION explicitly
    # (see Dockerfile).
    service_version: str = Field(default="dev")
    port: int = Field(default=8081, ge=1, le=65535)
    log_level: str = Field(default="INFO")
    # Mirrors the Go API's middleware.MaxBytes(4096) limit on POST
    # /api/v1/posts -- same contract, same default, independently
    # configurable per service.
    request_body_max_bytes: int = Field(default=4096, gt=0)


def get_settings() -> Settings:
    """Re-reads environment on every call rather than caching a module-level
    singleton -- this keeps tests free to monkeypatch environment variables
    per-test without import-order surprises. The cost (re-parsing a handful
    of env vars) is negligible for this service's traffic volume.
    """
    return Settings()
