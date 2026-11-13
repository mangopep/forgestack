from __future__ import annotations

from fastapi.testclient import TestClient


def test_create_and_list_item(client: TestClient) -> None:
    create_resp = client.post("/api/v1/items", json={"title": "write F3 tests"})
    assert create_resp.status_code == 201
    created = create_resp.json()
    assert created["title"] == "write F3 tests"
    assert isinstance(created["id"], int)

    list_resp = client.get("/api/v1/items")
    assert list_resp.status_code == 200
    items = list_resp.json()["items"]
    assert any(i["id"] == created["id"] for i in items)


def test_list_respects_limit(client: TestClient) -> None:
    for i in range(3):
        client.post("/api/v1/items", json={"title": f"item {i}"})
    resp = client.get("/api/v1/items", params={"limit": 1})
    assert resp.status_code == 200
    assert len(resp.json()["items"]) == 1


def test_create_rejects_empty_title(client: TestClient) -> None:
    resp = client.post("/api/v1/items", json={"title": ""})
    assert resp.status_code == 422
    assert "error" in resp.json()


def test_create_rejects_missing_title(client: TestClient) -> None:
    resp = client.post("/api/v1/items", json={})
    assert resp.status_code == 422
    assert "error" in resp.json()


def test_create_rejects_oversized_body(client: TestClient) -> None:
    huge_title = "x" * 5000
    resp = client.post("/api/v1/items", json={"title": huge_title})
    # Either the max-body-size middleware (413) or Pydantic's max_length
    # validation (422) should reject this -- both are correct depending on
    # which layer sees it first; the test asserts the contract (rejected,
    # with the standard error shape), not a specific layer's status code.
    assert resp.status_code in (413, 422)
    assert "error" in resp.json()
