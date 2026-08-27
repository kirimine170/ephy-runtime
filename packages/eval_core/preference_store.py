from __future__ import annotations

import json
import os
import sqlite3
from collections.abc import Mapping
from pathlib import Path

from .preference_schemas import (
    ConversationScenario,
    PreferencePair,
    PreferenceSession,
    PreferenceVote,
)


SCHEMA_VERSION = 1
DATA_ROOT_ENV = "EPHY_PREFERENCE_DATA_ROOT"


class PreferenceStore:
    def __init__(
        self,
        data_root: Path | None = None,
        *,
        environ: Mapping[str, str] | None = None,
    ) -> None:
        env = environ if environ is not None else os.environ
        configured = data_root if data_root is not None else (
            Path(env[DATA_ROOT_ENV]).expanduser() if env.get(DATA_ROOT_ENV) else None
        )
        if configured is not None and not configured.is_absolute():
            raise ValueError(f"{DATA_ROOT_ENV} must be an absolute path")
        self._data_root = configured.resolve(strict=False) if configured is not None else None

    @property
    def data_root(self) -> Path:
        if self._data_root is None:
            raise ValueError(
                f"Persistent preference operations require {DATA_ROOT_ENV}=/absolute/path"
            )
        return self._data_root

    @property
    def database_path(self) -> Path:
        return self.data_root / "preferences.sqlite3"

    def _connect(self) -> sqlite3.Connection:
        root = self.data_root
        root.mkdir(parents=True, exist_ok=True)
        database = self.database_path
        if database.exists() and database.is_symlink():
            raise ValueError("Preference database must not be a symlink")
        connection = sqlite3.connect(database, timeout=30)
        connection.row_factory = sqlite3.Row
        connection.execute("PRAGMA foreign_keys = ON")
        connection.execute("PRAGMA journal_mode = WAL")
        self._initialize(connection)
        return connection

    @staticmethod
    def _initialize(connection: sqlite3.Connection) -> None:
        connection.executescript(
            """
            CREATE TABLE IF NOT EXISTS metadata (
                key TEXT PRIMARY KEY,
                value TEXT NOT NULL
            );
            CREATE TABLE IF NOT EXISTS sessions (
                session_id TEXT PRIMARY KEY,
                payload_json TEXT NOT NULL,
                created_at TEXT NOT NULL
            );
            CREATE TABLE IF NOT EXISTS scenarios (
                session_id TEXT NOT NULL REFERENCES sessions(session_id) ON DELETE RESTRICT,
                scenario_id TEXT NOT NULL,
                ordinal INTEGER NOT NULL,
                payload_json TEXT NOT NULL,
                PRIMARY KEY (session_id, scenario_id)
            );
            CREATE TABLE IF NOT EXISTS generations (
                pair_id TEXT PRIMARY KEY,
                session_id TEXT NOT NULL REFERENCES sessions(session_id) ON DELETE RESTRICT,
                scenario_id TEXT NOT NULL,
                generation_index INTEGER NOT NULL,
                candidate_a_json TEXT NOT NULL,
                candidate_b_json TEXT NOT NULL,
                response_a TEXT NOT NULL,
                response_b TEXT NOT NULL,
                response_a_sha256 TEXT NOT NULL,
                response_b_sha256 TEXT NOT NULL,
                display_order TEXT NOT NULL CHECK(display_order IN ('ab', 'ba')),
                status TEXT NOT NULL CHECK(status IN ('pending', 'reviewed', 'duplicate_generation')),
                created_at TEXT NOT NULL,
                UNIQUE(session_id, generation_index),
                FOREIGN KEY(session_id, scenario_id) REFERENCES scenarios(session_id, scenario_id)
            );
            CREATE TABLE IF NOT EXISTS votes (
                vote_id TEXT PRIMARY KEY,
                pair_id TEXT NOT NULL REFERENCES generations(pair_id) ON DELETE RESTRICT,
                selection TEXT NOT NULL CHECK(selection IN ('a', 'b', 'tie', 'skip')),
                reason_tags_json TEXT NOT NULL,
                note TEXT,
                reviewer_type TEXT NOT NULL CHECK(reviewer_type IN ('human', 'llm')),
                approved_for_sft INTEGER NOT NULL CHECK(approved_for_sft IN (0, 1)),
                created_at TEXT NOT NULL,
                supersedes_vote_id TEXT UNIQUE REFERENCES votes(vote_id) ON DELETE RESTRICT
            );
            CREATE INDEX IF NOT EXISTS idx_generations_session_status
                ON generations(session_id, status, generation_index);
            CREATE INDEX IF NOT EXISTS idx_votes_pair_created
                ON votes(pair_id, created_at);
            """
        )
        existing = connection.execute(
            "SELECT value FROM metadata WHERE key = 'schema_version'"
        ).fetchone()
        if existing is None:
            connection.execute(
                "INSERT INTO metadata(key, value) VALUES('schema_version', ?)",
                (str(SCHEMA_VERSION),),
            )
        elif int(existing["value"]) != SCHEMA_VERSION:
            raise ValueError("Unsupported preference database schema version")
        connection.commit()

    def create_session(
        self,
        session: PreferenceSession,
        scenarios: list[ConversationScenario],
    ) -> PreferenceSession:
        if not scenarios:
            raise ValueError("Preference dataset must contain at least one scenario")
        with self._connect() as connection:
            connection.execute("BEGIN IMMEDIATE")
            connection.execute(
                "INSERT INTO sessions(session_id, payload_json, created_at) VALUES(?, ?, ?)",
                (session.session_id, session.model_dump_json(), session.created_at.isoformat()),
            )
            connection.executemany(
                """
                INSERT INTO scenarios(session_id, scenario_id, ordinal, payload_json)
                VALUES(?, ?, ?, ?)
                """,
                [
                    (session.session_id, scenario.scenario_id, ordinal, scenario.model_dump_json())
                    for ordinal, scenario in enumerate(scenarios)
                ],
            )
        return session

    def get_session(self, session_id: str) -> PreferenceSession:
        with self._connect() as connection:
            row = connection.execute(
                "SELECT payload_json FROM sessions WHERE session_id = ?", (session_id,)
            ).fetchone()
        if row is None:
            raise ValueError("Unknown preference session")
        return PreferenceSession.model_validate_json(row["payload_json"])

    def list_sessions(self) -> list[PreferenceSession]:
        with self._connect() as connection:
            rows = connection.execute(
                "SELECT payload_json FROM sessions ORDER BY created_at DESC"
            ).fetchall()
        return [PreferenceSession.model_validate_json(row["payload_json"]) for row in rows]

    def generation_count(self, session_id: str) -> int:
        with self._connect() as connection:
            row = connection.execute(
                "SELECT COUNT(*) AS count FROM generations WHERE session_id = ?", (session_id,)
            ).fetchone()
        return int(row["count"])

    def scenario_for_generation(self, session_id: str, generation_index: int) -> ConversationScenario:
        with self._connect() as connection:
            count_row = connection.execute(
                "SELECT COUNT(*) AS count FROM scenarios WHERE session_id = ?", (session_id,)
            ).fetchone()
            count = int(count_row["count"])
            if count == 0:
                raise ValueError("Preference session has no scenarios")
            row = connection.execute(
                """
                SELECT payload_json FROM scenarios
                WHERE session_id = ? AND ordinal = ?
                """,
                (session_id, generation_index % count),
            ).fetchone()
        return ConversationScenario.model_validate_json(row["payload_json"])

    def add_pair(self, pair: PreferencePair, generation_index: int) -> PreferencePair:
        with self._connect() as connection:
            connection.execute("BEGIN IMMEDIATE")
            connection.execute(
                """
                INSERT INTO generations(
                    pair_id, session_id, scenario_id, generation_index,
                    candidate_a_json, candidate_b_json, response_a, response_b,
                    response_a_sha256, response_b_sha256, display_order, status, created_at
                ) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
                """,
                (
                    pair.pair_id,
                    pair.session_id,
                    pair.scenario_id,
                    generation_index,
                    pair.candidate_a.model_dump_json(),
                    pair.candidate_b.model_dump_json(),
                    pair.response_a,
                    pair.response_b,
                    pair.response_a_sha256,
                    pair.response_b_sha256,
                    pair.display_order,
                    pair.status,
                    pair.created_at.isoformat(),
                ),
            )
        return pair

    @staticmethod
    def _pair_from_row(row: sqlite3.Row) -> PreferencePair:
        payload = {
            "pair_id": row["pair_id"],
            "session_id": row["session_id"],
            "scenario_id": row["scenario_id"],
            "candidate_a": json.loads(row["candidate_a_json"]),
            "candidate_b": json.loads(row["candidate_b_json"]),
            "response_a": row["response_a"],
            "response_b": row["response_b"],
            "response_a_sha256": row["response_a_sha256"],
            "response_b_sha256": row["response_b_sha256"],
            "display_order": row["display_order"],
            "status": row["status"],
            "created_at": row["created_at"],
        }
        return PreferencePair.model_validate(payload)

    def get_pair(self, pair_id: str) -> PreferencePair:
        with self._connect() as connection:
            row = connection.execute(
                "SELECT * FROM generations WHERE pair_id = ?", (pair_id,)
            ).fetchone()
        if row is None:
            raise ValueError("Unknown preference pair")
        return self._pair_from_row(row)

    def get_scenario(self, session_id: str, scenario_id: str) -> ConversationScenario:
        with self._connect() as connection:
            row = connection.execute(
                """
                SELECT payload_json FROM scenarios
                WHERE session_id = ? AND scenario_id = ?
                """,
                (session_id, scenario_id),
            ).fetchone()
        if row is None:
            raise ValueError("Unknown preference scenario")
        return ConversationScenario.model_validate_json(row["payload_json"])

    def next_pair(self, session_id: str) -> PreferencePair | None:
        with self._connect() as connection:
            row = connection.execute(
                """
                SELECT * FROM generations
                WHERE session_id = ? AND status = 'pending'
                ORDER BY generation_index ASC LIMIT 1
                """,
                (session_id,),
            ).fetchone()
        return self._pair_from_row(row) if row is not None else None

    def latest_vote(self, pair_id: str) -> PreferenceVote | None:
        with self._connect() as connection:
            row = connection.execute(
                """
                SELECT current.* FROM votes AS current
                WHERE current.pair_id = ?
                  AND NOT EXISTS (
                    SELECT 1 FROM votes AS newer
                    WHERE newer.supersedes_vote_id = current.vote_id
                  )
                ORDER BY current.created_at DESC LIMIT 1
                """,
                (pair_id,),
            ).fetchone()
        return self._vote_from_row(row) if row is not None else None

    @staticmethod
    def _vote_from_row(row: sqlite3.Row) -> PreferenceVote:
        return PreferenceVote.model_validate(
            {
                "vote_id": row["vote_id"],
                "pair_id": row["pair_id"],
                "selection": row["selection"],
                "reason_tags": json.loads(row["reason_tags_json"]),
                "note": row["note"],
                "reviewer_type": row["reviewer_type"],
                "approved_for_sft": bool(row["approved_for_sft"]),
                "created_at": row["created_at"],
                "supersedes_vote_id": row["supersedes_vote_id"],
            }
        )

    def add_vote(self, vote: PreferenceVote) -> PreferenceVote:
        with self._connect() as connection:
            connection.execute("BEGIN IMMEDIATE")
            pair = connection.execute(
                "SELECT status FROM generations WHERE pair_id = ?", (vote.pair_id,)
            ).fetchone()
            if pair is None:
                raise ValueError("Unknown preference pair")
            if pair["status"] == "duplicate_generation":
                raise ValueError("Duplicate generations cannot be reviewed")
            latest = connection.execute(
                """
                SELECT current.vote_id FROM votes AS current
                WHERE current.pair_id = ?
                  AND NOT EXISTS (
                    SELECT 1 FROM votes AS newer
                    WHERE newer.supersedes_vote_id = current.vote_id
                  )
                ORDER BY current.created_at DESC LIMIT 1
                """,
                (vote.pair_id,),
            ).fetchone()
            if latest is None and vote.supersedes_vote_id is not None:
                raise ValueError("There is no vote to supersede")
            if latest is not None and vote.supersedes_vote_id != latest["vote_id"]:
                raise ValueError("Pair already has a vote; supersede the latest vote to correct it")
            connection.execute(
                """
                INSERT INTO votes(
                    vote_id, pair_id, selection, reason_tags_json, note,
                    reviewer_type, approved_for_sft, created_at, supersedes_vote_id
                ) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)
                """,
                (
                    vote.vote_id,
                    vote.pair_id,
                    vote.selection,
                    json.dumps(vote.reason_tags, ensure_ascii=False),
                    vote.note,
                    vote.reviewer_type,
                    int(vote.approved_for_sft),
                    vote.created_at.isoformat(),
                    vote.supersedes_vote_id,
                ),
            )
            connection.execute(
                "UPDATE generations SET status = 'reviewed' WHERE pair_id = ?",
                (vote.pair_id,),
            )
        return vote

    def session_rows(self, session_id: str) -> list[dict]:
        with self._connect() as connection:
            rows = connection.execute(
                """
                SELECT generation.*, scenario.payload_json AS scenario_json,
                       vote.vote_id, vote.selection, vote.reason_tags_json, vote.note,
                       vote.reviewer_type, vote.approved_for_sft,
                       vote.created_at AS vote_created_at, vote.supersedes_vote_id
                FROM generations AS generation
                JOIN scenarios AS scenario
                  ON scenario.session_id = generation.session_id
                 AND scenario.scenario_id = generation.scenario_id
                LEFT JOIN votes AS vote
                  ON vote.pair_id = generation.pair_id
                 AND NOT EXISTS (
                    SELECT 1 FROM votes AS newer
                    WHERE newer.supersedes_vote_id = vote.vote_id
                 )
                WHERE generation.session_id = ?
                ORDER BY generation.generation_index ASC
                """,
                (session_id,),
            ).fetchall()
        result = []
        for row in rows:
            vote = None
            if row["vote_id"] is not None:
                vote = PreferenceVote.model_validate(
                    {
                        "vote_id": row["vote_id"],
                        "pair_id": row["pair_id"],
                        "selection": row["selection"],
                        "reason_tags": json.loads(row["reason_tags_json"]),
                        "note": row["note"],
                        "reviewer_type": row["reviewer_type"],
                        "approved_for_sft": bool(row["approved_for_sft"]),
                        "created_at": row["vote_created_at"],
                        "supersedes_vote_id": row["supersedes_vote_id"],
                    }
                )
            result.append(
                {
                    "pair": self._pair_from_row(row),
                    "scenario": ConversationScenario.model_validate_json(row["scenario_json"]),
                    "vote": vote,
                }
            )
        return result

    def vote_history(self, pair_id: str) -> list[PreferenceVote]:
        with self._connect() as connection:
            rows = connection.execute(
                "SELECT * FROM votes WHERE pair_id = ? ORDER BY created_at ASC", (pair_id,)
            ).fetchall()
        return [self._vote_from_row(row) for row in rows]
