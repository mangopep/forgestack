from __future__ import annotations

from fastapi import APIRouter, Request

from app.schemas import Item, ItemCreate, ItemList
from app.store import InMemoryItemStore

router = APIRouter()


def get_store(request: Request) -> InMemoryItemStore:
    """FastAPI-idiomatic dependency injection: the store is created once in
    the app's lifespan (see main.py) and handed to each request via
    app.state, not re-instantiated per request and not wired through a
    custom DI container -- justified here only because it's the framework's
    own established pattern, not an abstraction added for its own sake.
    """
    store: InMemoryItemStore = request.app.state.store
    return store


@router.get("/api/v1/items", response_model=ItemList)
async def list_items(request: Request, limit: int | None = None) -> ItemList:
    store = get_store(request)
    return ItemList(items=store.list(limit=limit))


@router.post("/api/v1/items", response_model=Item, status_code=201)
async def create_item(request: Request, body: ItemCreate) -> Item:
    store = get_store(request)
    return store.create(title=body.title)
