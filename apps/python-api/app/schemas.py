"""Typed request/response schemas. Mirrors the Go API's posts module
conventions (an "item" with a required, length-bounded title) closely
enough to demonstrate the same validation contract in both languages,
without claiming to be the same domain.
"""

from __future__ import annotations

from pydantic import BaseModel, Field


class ItemCreate(BaseModel):
    title: str = Field(min_length=1, max_length=200)


class Item(BaseModel):
    id: int
    title: str


class ItemList(BaseModel):
    items: list[Item]
