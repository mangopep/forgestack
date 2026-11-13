from __future__ import annotations

from fastapi.testclient import TestClient


def test_healthz(client: TestClient) -> None:
    resp = client.get("/healthz")
    assert resp.status_code == 200
    body = resp.json()
    assert body["status"] == "ok"
    assert "version" in body


def test_readyz(client: TestClient) -> None:
    resp = client.get("/readyz")
    assert resp.status_code == 200
    assert resp.json() == {"status": "ready"}


def test_every_response_carries_a_correlation_id(client: TestClient) -> None:
    resp = client.get("/healthz")
    assert "x-request-id" in resp.headers
    assert len(resp.headers["x-request-id"]) > 0


def test_correlation_id_is_reused_when_supplied(client: TestClient) -> None:
    resp = client.get("/healthz", headers={"X-Request-Id": "test-trace-123"})
    assert resp.headers["x-request-id"] == "test-trace-123"
