from __future__ import annotations

from fastapi.testclient import TestClient


def test_not_found_uses_standard_error_shape(client: TestClient) -> None:
    resp = client.get("/does-not-exist")
    assert resp.status_code == 404
    assert resp.json() == {"error": "Not Found"}


def test_invalid_json_body_is_rejected(client: TestClient) -> None:
    resp = client.post(
        "/api/v1/items",
        content=b"not json",
        headers={"Content-Type": "application/json"},
    )
    assert resp.status_code == 422
    assert "error" in resp.json()
