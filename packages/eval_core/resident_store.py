"""Versioned resident tables in the existing private PreferenceStore database．"""
from __future__ import annotations

from contextlib import closing, contextmanager
import json
import sqlite3

from .preference_store import PreferenceStore

SCHEMA_VERSION = 1


class ResidentStore:
    def __init__(self, preference_store: PreferenceStore):
        self.preference_store = preference_store

    @contextmanager
    def transaction(self):
        with closing(self.preference_store._connect()) as connection:
            self._initialize(connection)
            with connection:
                connection.execute("BEGIN IMMEDIATE")
                yield connection

    @staticmethod
    def _initialize(connection: sqlite3.Connection):
        existing = connection.execute("SELECT value FROM metadata WHERE key='resident_schema_version'").fetchone()
        if existing is not None and int(existing["value"]) != SCHEMA_VERSION:
            raise ValueError("Unsupported resident feedback schema version")
        connection.executescript("""
            CREATE TABLE IF NOT EXISTS resident_instances (
                instance_id TEXT NOT NULL, owner_key TEXT NOT NULL, revision INTEGER NOT NULL DEFAULT 0,
                PRIMARY KEY(instance_id, owner_key)
            );
            CREATE TABLE IF NOT EXISTS resident_sessions (
                session_id TEXT NOT NULL, instance_id TEXT NOT NULL, owner_key TEXT NOT NULL,
                owner_selected INTEGER NOT NULL, storage_consent INTEGER NOT NULL, created_at TEXT NOT NULL,
                PRIMARY KEY(instance_id, owner_key, session_id)
            );
            CREATE TABLE IF NOT EXISTS resident_feedback (
                event_id TEXT PRIMARY KEY, instance_id TEXT NOT NULL, owner_key TEXT NOT NULL,
                session_id TEXT NOT NULL, dedupe_id TEXT NOT NULL, request_hash TEXT NOT NULL,
                created_at TEXT NOT NULL, payload_json TEXT NOT NULL,
                UNIQUE(instance_id, owner_key, session_id, dedupe_id)
            );
            CREATE TABLE IF NOT EXISTS resident_changes (
                change_id TEXT PRIMARY KEY, instance_id TEXT NOT NULL, owner_key TEXT NOT NULL,
                session_id TEXT NOT NULL, revision INTEGER NOT NULL, field TEXT NOT NULL,
                scope TEXT NOT NULL, value_json TEXT NOT NULL, payload_json TEXT NOT NULL,
                UNIQUE(instance_id, owner_key, revision)
            );
            CREATE TABLE IF NOT EXISTS resident_operations (
                instance_id TEXT NOT NULL, owner_key TEXT NOT NULL, session_id TEXT NOT NULL,
                dedupe_id TEXT NOT NULL, request_hash TEXT NOT NULL, result_json TEXT NOT NULL,
                PRIMARY KEY(instance_id, owner_key, session_id, dedupe_id)
            );
            CREATE INDEX IF NOT EXISTS resident_feedback_history ON resident_feedback(instance_id, owner_key, created_at);
            CREATE INDEX IF NOT EXISTS resident_change_history ON resident_changes(instance_id, owner_key, revision);
        """)
        connection.execute("INSERT OR IGNORE INTO metadata(key,value) VALUES('resident_schema_version', ?)", (str(SCHEMA_VERSION),))
        connection.commit()


def encode(value):
    return json.dumps(value, ensure_ascii=False, sort_keys=True, separators=(",", ":"))
