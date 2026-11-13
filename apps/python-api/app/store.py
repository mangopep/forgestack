"""In-memory, thread-safe item store -- deliberately the simplest possible
storage (a list behind a lock), mirroring the Go API's
repository.NewInMemory(). No database is introduced here; that belongs to
a later phase per the F3 scope ("do not prematurely implement...
databases").
"""

from __future__ import annotations

import threading

from app.schemas import Item


class InMemoryItemStore:
    def __init__(self) -> None:
        self._lock = threading.Lock()
        self._items: list[Item] = []
        self._next_id = 1

    def create(self, title: str) -> Item:
        with self._lock:
            item = Item(id=self._next_id, title=title)
            self._next_id += 1
            self._items.append(item)
            return item

    def list(self, limit: int | None = None) -> list[Item]:
        with self._lock:
            items = list(self._items)
        if limit is not None:
            return items[:limit]
        return items
