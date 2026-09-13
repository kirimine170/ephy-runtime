"""Single owner of resident feedback attribution and atomic profile revisions．"""
from __future__ import annotations

from datetime import datetime, timedelta, timezone
import hashlib
import json
from pathlib import Path
import re
from uuid import uuid4

from packages.config_core.loader import ROOT_DIR
from packages.profile_core.resident import ProfileChange, ResidentPolicy, apply_resident_policy
from .preference_store import PreferenceStore
from .resident_schemas import FeedbackRequest, ResidentSessionRequest, RetractRequest, UndoRequest
from .resident_store import ResidentStore, encode


class ResidentConflict(ValueError):
    pass


def classify_feedback(request: FeedbackRequest) -> dict:
    """Deliberately anchored owner commands; quotations and tool text are not commands．"""
    text = re.sub(r"[。．.!！?？\s]+$", "", request.text.strip())
    result = {"recognized": request.kind is not None, "stop_requested": False,
              "kind": request.kind or "neutral", "change": None, "undo": False}
    if any(c in text for c in '「」『』“”"`\n'):
        return result
    permanent = text.startswith(("今後も", "これからも", "いつも"))
    clean = re.sub(r"^(?:今後も|これからも|いつも)[，、\s]*", "", text)
    scope = "owner" if permanent else "session"
    if text in {"止めて", "とめて", "停止して", "ストップ", "待って"}:
        result.update(recognized=True, stop_requested=True, kind="negative")
    elif text in {"今は話しかけないで", "しばらく話しかけないで"}:
        result.update(recognized=True, stop_requested=True, kind="negative",
                      change=ProfileChange(field="proactive", value="suppressed"))
    elif clean in {"もっと短くして", "短くして", "説明が長い", "返答が長い"}:
        result.update(recognized=True, kind="negative",
                      change=ProfileChange(field="response_length", value="brief", scope=scope))
    elif clean in {"呼びかけを減らして", "名前を呼ぶ回数を減らして"}:
        result.update(recognized=True, kind="negative",
                      change=ProfileChange(field="call_name_frequency", value="low", scope=scope))
    elif clean == "名前を呼ばないで":
        result.update(recognized=True, kind="negative",
                      change=ProfileChange(field="call_name_frequency", value="never", scope=scope))
    elif text in {"今の言い方はよかった", "今の言い方は良かった", "よかった", "良かった"}:
        result.update(recognized=True, kind="positive")
    elif text in {"嫌だった", "今のは嫌だった", "今の声が嫌だった"}:
        result.update(recognized=True, kind="negative")
    elif text == "それは人前で言わないで":
        memories = request.target.memory_ids if request.target else []
        result.update(recognized=True, kind="negative", needs_target=not bool(memories))
        if memories:
            result["change"] = ProfileChange(field="restricted_memory_ids", value=memories, scope="owner")
    elif text in {"さっきの変更を戻して", "最後の変更を戻して"}:
        result.update(recognized=True, undo=True)
    elif re.fullmatch(r"違う[，、,]?それは.{1,300}", text):
        result.update(recognized=True, kind="correction", needs_target=not bool(request.target and request.target.memory_ids))
    if request.change is not None and request.input_source == "ui":
        result.update(recognized=True, change=request.change)
    return result


class ResidentService:
    def __init__(self, *, store: PreferenceStore | None = None, instance_id: str,
                 owner_key: str = "selected-owner", repository_root: Path = ROOT_DIR,
                 clock=None, retention_days: int = 90, max_records: int = 10000):
        self.store = ResidentStore(store or PreferenceStore())
        self.instance_id = instance_id
        self.owner_key = owner_key
        self.repository_root = repository_root
        self.clock = clock or (lambda: datetime.now(timezone.utc))
        self.retention_days = max(1, min(retention_days, 3650))
        self.max_records = max(1, min(max_records, 100000))
        self.boot_id = str(uuid4())
        self._volatile: dict[str, dict] = {}

    @property
    def key(self):
        return (self.instance_id, self.owner_key)

    def _prepare(self):
        self.store.preference_store.ensure_private_root(self.repository_root)

    def create_session(self, request: ResidentSessionRequest) -> dict:
        self._prepare()
        with self.store.transaction() as db:
            db.execute("INSERT OR IGNORE INTO resident_instances(instance_id,owner_key) VALUES(?,?)", self.key)
            existing = db.execute("SELECT * FROM resident_sessions WHERE instance_id=? AND owner_key=? AND session_id=?",
                                  (*self.key, request.session_id)).fetchone()
            if existing is not None:
                if bool(existing["owner_selected"]) != request.owner_selected:
                    self._volatile.pop(request.session_id, None)
                    # UI selection is an explicit trust boundary; clear the old session's overrides．
                    for field, previous in self._layers(db, existing)["session"].items():
                        if previous["after"] is not None:
                            self._add_change(db, existing, ProfileChange(field=field, value=ResidentPolicy().model_dump()[field]),
                                             None, restore={"value": None})
                db.execute("UPDATE resident_sessions SET owner_selected=?,storage_consent=? WHERE instance_id=? AND owner_key=? AND session_id=?",
                           (int(request.owner_selected), int(request.storage_consent), *self.key, request.session_id))
            else:
                if db.execute("SELECT count(*) FROM resident_sessions WHERE instance_id=? AND owner_key=?", self.key).fetchone()[0] >= 1024:
                    raise ValueError("Resident session capacity reached")
                db.execute("INSERT INTO resident_sessions VALUES(?,?,?,?,?,?)",
                           (request.session_id, *self.key, int(request.owner_selected), int(request.storage_consent), self.clock().isoformat()))
            return self._state(db, request.session_id)

    def _session(self, db, session_id):
        session = db.execute("SELECT * FROM resident_sessions WHERE instance_id=? AND owner_key=? AND session_id=?",
                             (*self.key, session_id)).fetchone()
        if session is None:
            raise ValueError("Unknown resident session")
        return session

    def _revision(self, db):
        row = db.execute("SELECT revision FROM resident_instances WHERE instance_id=? AND owner_key=?", self.key).fetchone()
        return row[0] if row else 0

    def _changes(self, db):
        return [json.loads(row[0]) for row in db.execute(
            "SELECT payload_json FROM resident_changes WHERE instance_id=? AND owner_key=? ORDER BY revision", self.key)]

    def _layers(self, db, session):
        layers = {"owner": {}, "expiring": {}, "session": {}}
        for change in self._changes(db):
            if change["scope"] == "session" and change["session_id"] != session["session_id"]:
                continue
            if change["scope"] == "session" and change.get("boot_id") != self.boot_id:
                continue
            if change["scope"] != "session" and not session["owner_selected"]:
                continue
            layers[change["scope"]][change["field"]] = change
        return layers

    def _state(self, db, session_id):
        session = self._session(db, session_id)
        layers = self._layers(db, session)
        values = ResidentPolicy().model_dump()
        now = self.clock()
        for scope in ("owner", "expiring", "session"):
            for field, change in layers[scope].items():
                if change["after"] is None:
                    continue
                if change.get("expires_at") and datetime.fromisoformat(change["expires_at"]) <= now:
                    continue
                values[field] = change["after"]
        values.update(self._volatile.get(session_id, {}))
        policy = ResidentPolicy.model_validate(values).model_dump()
        history_scope = "" if session["owner_selected"] else " AND session_id=?"
        history_parameters = self.key if session["owner_selected"] else (*self.key, session_id)
        feedback = [json.loads(row[0]) for row in db.execute(
            "SELECT payload_json FROM resident_feedback WHERE instance_id=? AND owner_key=?" + history_scope + " ORDER BY created_at DESC LIMIT 100",
            history_parameters)]
        changes = [c for c in self._changes(db) if c["session_id"] == session_id or (session["owner_selected"] and c["scope"] != "session")]
        for change in changes:
            ended = change["scope"] == "session" and change.get("boot_id") != self.boot_id
            expired = change.get("expires_at") and datetime.fromisoformat(change["expires_at"]) <= now
            if change["status"] == "applied" and (ended or expired):
                change["status"] = "expired"
        return {"session_id": session_id, "revision": self._revision(db), "policy": policy,
                "policy_snapshot_id": "resident-v1:" + hashlib.sha256(encode(policy).encode()).hexdigest()[:24],
                "feedback": feedback, "changes": list(reversed(changes[-100:])),
                "owner_selected": bool(session["owner_selected"]), "storage_consent": bool(session["storage_consent"]),
                "training_enabled": False, "retention_days": self.retention_days, "max_records": self.max_records}

    def state(self, session_id):
        self._prepare()
        with self.store.transaction() as db:
            self._expire_payloads(db)
            return self._state(db, session_id)

    @staticmethod
    def apply_policy(payload, snapshot):
        return apply_resident_policy(payload, ResidentPolicy.model_validate(snapshot["policy"]))

    def _expire_payloads(self, db):
        cutoff = (self.clock() - timedelta(days=self.retention_days)).isoformat()
        for row in db.execute("SELECT event_id,payload_json FROM resident_feedback WHERE instance_id=? AND owner_key=? AND created_at<?", (*self.key, cutoff)):
            item = json.loads(row["payload_json"])
            if not item.get("content_expired"):
                item.update(text="", target=None, participant_scope=[], training_consent=False, content_expired=True)
                db.execute("UPDATE resident_feedback SET payload_json=? WHERE event_id=?", (encode(item), row["event_id"]))

    def _capacity(self, db):
        counts = [db.execute(f"SELECT count(*) FROM {table} WHERE instance_id=? AND owner_key=?", self.key).fetchone()[0]
                  for table in ("resident_feedback", "resident_changes", "resident_operations")]
        if any(count >= self.max_records for count in counts):
            raise ValueError("Resident history capacity reached; no feedback was saved")

    def _add_change(self, db, session, candidate, event_id, *, undo_of=None, restore=None):
        current = self._layers(db, session)[candidate.scope].get(candidate.field)
        before = current["after"] if current else None
        after = candidate.value if restore is None else restore["value"]
        if candidate.field == "restricted_memory_ids" and restore is None:
            # A new suppression may narrow sharing; never replace unrelated restrictions．
            effective = self._state(db, session["session_id"])["policy"][candidate.field]
            after = list(dict.fromkeys([*effective, *candidate.value]))
            ResidentPolicy.model_validate({candidate.field: after})
        expires_at = candidate.expires_at.isoformat() if candidate.expires_at else None
        if restore is not None:
            expires_at = restore.get("expires_at")
        effective = self._state(db, session["session_id"])["policy"][candidate.field]
        if undo_of is None and after == effective and (current is not None or candidate.scope == "session"):
            return {"status": "no_change", "field": candidate.field, "before": effective, "after": after,
                    "scope": candidate.scope, "revision": self._revision(db), "change_id": None}
        revision = self._revision(db) + 1
        change = {"change_id": str(uuid4()), "session_id": session["session_id"], "source_feedback_id": event_id,
                  "field": candidate.field, "before": before, "after": after, "scope": candidate.scope,
                  "before_expires_at": current.get("expires_at") if current else None, "expires_at": expires_at,
                  "applied_at": self.clock().isoformat(), "revision": revision,
                  "boot_id": self.boot_id,
                  "status": "reverted" if undo_of else "applied", "undo_of": undo_of}
        db.execute("INSERT INTO resident_changes VALUES(?,?,?,?,?,?,?,?,?)",
                   (change["change_id"], *self.key, session["session_id"], revision, candidate.field, candidate.scope,
                    encode(after), encode(change)))
        db.execute("UPDATE resident_instances SET revision=? WHERE instance_id=? AND owner_key=?", (revision, *self.key))
        return change

    def submit(self, request: FeedbackRequest):
        intent = classify_feedback(request)
        if not intent["recognized"]:
            return {"recognized": False, "stop_requested": False, "feedback": None, "change": None,
                    "state": self.state(request.session_id)}
        self._prepare()
        # The timestamp may be freshly defaulted by a retried HTTP request．
        digest = hashlib.sha256(encode(request.model_dump(mode="json", exclude={"observed_at"})).encode()).hexdigest()
        with self.store.transaction() as db:
            session = self._session(db, request.session_id)
            if not session["storage_consent"]:
                candidate = intent["change"]
                change = {"status": "unsaved", "reason": "storage_consent_required"}
                if candidate and candidate.scope == "session" and session["owner_selected"] and request.speaker == "owner":
                    if request.expected_revision != self._revision(db):
                        change = {"status": "failed", "reason": "stale_revision"}
                    else:
                        before = self._state(db, request.session_id)["policy"][candidate.field]
                        self._volatile.setdefault(request.session_id, {})[candidate.field] = candidate.value
                        if before != candidate.value:
                            db.execute("UPDATE resident_instances SET revision=revision+1 WHERE instance_id=? AND owner_key=?", self.key)
                        change.update(scope="session", field=candidate.field, before=before, after=candidate.value,
                                      revision=self._revision(db), reason="session_only_without_storage")
                return {"recognized": True, "stop_requested": intent["stop_requested"], "feedback": None,
                        "change": change, "saved": False, "state": self._state(db, request.session_id)}
            old = db.execute("SELECT payload_json,request_hash FROM resident_feedback WHERE instance_id=? AND owner_key=? AND session_id=? AND dedupe_id=?",
                             (*self.key, request.session_id, request.dedupe_id)).fetchone()
            if old:
                if old["request_hash"] != digest:
                    raise ResidentConflict("Idempotency key was reused with different feedback")
                event = json.loads(old["payload_json"])
                change = next((c for c in self._changes(db) if c["change_id"] == event.get("change_id")), None)
                return {"recognized": True, "stop_requested": intent["stop_requested"], "feedback": event,
                        "change": change, "state": self._state(db, request.session_id), "duplicate": True}
            self._capacity(db)
            self._expire_payloads(db)
            event_id = str(uuid4())
            event = request.model_dump(mode="json", exclude={"change"})
            event.update(event_id=event_id, instance_id=self.instance_id, kind=intent["kind"],
                         status="recorded", storage_consent=True, training_consent=False,
                         provenance="human_ui" if request.input_source == "ui" else "asr_transcript",
                         export_eligible=False, export_exclusion_reason="single_feedback_is_not_an_approved_pair_or_sft_example",
                         deleted=False, change_id=None, effective_revision=self._revision(db),
                         stop_requested=intent["stop_requested"])
            event["effective_policy_id"] = self._state(db, request.session_id)["policy_snapshot_id"]
            # Unknown generation artifacts stay unknown; this is the feedback-time policy only．
            event["prompt_id"] = request.prompt_id or "unknown"
            event["configuration_id"] = request.configuration_id or "unknown"
            event["dimension"] = ("voice_quality" if request.text.rstrip("。．.!！?？ ") == "今の声が嫌だった"
                                  else "factual_correction" if intent["kind"] == "correction"
                                  else "response_length" if intent["change"] and intent["change"].field == "response_length"
                                  else "unclassified")
            target_known = request.target is not None and bool(request.target.turn_id or request.target.run_id or request.target.speech_unit_ids)
            if intent.get("needs_target") or (not target_known and intent["change"] is None and not intent["stop_requested"] and not intent["undo"]):
                event["status"] = "needs_clarification"
                event["clarification_reason"] = "target_unknown"
            candidate = intent["change"]
            change = None
            if candidate is not None:
                event["proposed_change"] = candidate.model_dump(mode="json")
                trusted = session["owner_selected"] and request.speaker == "owner"
                if not trusted:
                    event["status"] = "needs_clarification"
                    event["clarification_reason"] = "owner_not_confirmed"
                    change = {"status": "pending", "reason": "owner_not_confirmed"}
                elif request.expected_revision != self._revision(db):
                    change = {"status": "failed", "reason": "stale_revision", "revision": self._revision(db)}
                elif candidate.expires_at and candidate.expires_at <= self.clock():
                    change = {"status": "failed", "reason": "expired"}
                else:
                    change = self._add_change(db, session, candidate, event_id)
            if intent["undo"]:
                if not session["owner_selected"] or request.speaker != "owner":
                    event["status"] = "needs_clarification"
                    event["clarification_reason"] = "owner_not_confirmed"
                elif request.expected_revision != self._revision(db):
                    change = {"status": "failed", "reason": "stale_revision"}
                else:
                    eligible = [c for c in self._changes(db) if c["session_id"] == request.session_id and c["status"] == "applied"]
                    if eligible:
                        try:
                            change = self._undo_change(db, session, eligible[-1]["change_id"], event_id)
                        except ResidentConflict:
                            change = {"status": "failed", "reason": "change_superseded"}
                    else:
                        event["status"] = "needs_clarification"
            if intent["kind"] == "correction":
                event["correction_status"] = "karte_review_required"
            if change is not None:
                event["change_id"] = change.get("change_id")
                event["application_status"] = change["status"]
                event["effective_revision"] = self._revision(db)
            db.execute("INSERT INTO resident_feedback VALUES(?,?,?,?,?,?,?,?)",
                       (event_id, *self.key, request.session_id, request.dedupe_id, digest, self.clock().isoformat(), encode(event)))
            return {"recognized": True, "stop_requested": intent["stop_requested"], "feedback": event,
                    "change": change, "state": self._state(db, request.session_id)}

    def _undo_change(self, db, session, change_id, event_id=None):
        changes = self._changes(db)
        target = next((c for c in changes if c["change_id"] == change_id), None)
        if target is None or (target["scope"] == "session" and target["session_id"] != session["session_id"]):
            raise ValueError("Unknown resident change")
        if not session["owner_selected"]:
            raise ValueError("Owner selection is required")
        current = self._layers(db, session)[target["scope"]].get(target["field"])
        memory_change = target["field"] == "restricted_memory_ids"
        newer_applied = any(c["revision"] > target["revision"] and c["field"] == target["field"]
                            and c["scope"] == target["scope"] and c["status"] == "applied"
                            and (c["scope"] != "session" or c["session_id"] == target["session_id"])
                            for c in changes)
        if current is None or (not memory_change and (current["after"] != target["after"] or newer_applied)):
            raise ResidentConflict("The targeted field has a newer change")
        if target["status"] != "applied":
            raise ResidentConflict("The change is already reverted")
        candidate = ProfileChange(field=target["field"], value=ResidentPolicy().model_dump()[target["field"]],
                                  scope=target["scope"], expires_at=target.get("expires_at"))
        restore = {"value": target["before"], "expires_at": target.get("before_expires_at")}
        if memory_change:
            added = set(target["after"] or []) - set(target["before"] or [])
            restore["value"] = [item for item in (current["after"] or []) if item not in added]
        elif target["before"] is not None:
            # Deleting an earlier instruction must not let a subsequent undo resurrect it．
            retracted = {row[0] for row in db.execute(
                "SELECT event_id FROM resident_feedback WHERE instance_id=? AND owner_key=? AND json_extract(payload_json,'$.status')='retracted'", self.key)}
            previous = [c for c in changes if c["revision"] < target["revision"]
                        and c["field"] == target["field"] and c["scope"] == target["scope"]
                        and (c["scope"] != "session" or (c["session_id"] == target["session_id"] and c.get("boot_id") == self.boot_id))
                        and c.get("source_feedback_id") not in retracted]
            if previous:
                restore = {"value": previous[-1]["after"], "expires_at": previous[-1].get("expires_at")}
            else:
                restore = {"value": None}
        change = self._add_change(db, session, candidate, event_id, undo_of=change_id,
                                  restore=restore)
        target["status"] = "reverted"
        target["reverted_by"] = change["change_id"]
        db.execute("UPDATE resident_changes SET payload_json=? WHERE change_id=?", (encode(target), change_id))
        return change

    def _operation(self, request, action, target_id, callback):
        self._prepare()
        digest = hashlib.sha256(encode([action, target_id, request.model_dump(mode="json")]).encode()).hexdigest()
        with self.store.transaction() as db:
            session = self._session(db, request.session_id)
            if not session["owner_selected"]:
                raise ValueError("Owner selection is required")
            old = db.execute("SELECT request_hash,result_json FROM resident_operations WHERE instance_id=? AND owner_key=? AND session_id=? AND dedupe_id=?",
                             (*self.key, request.session_id, request.dedupe_id)).fetchone()
            if old:
                if old[0] != digest:
                    raise ResidentConflict("Idempotency key was reused")
                result = json.loads(old[1])
                return {**result, "state": self._state(db, request.session_id), "duplicate": True}
            if request.expected_revision != self._revision(db):
                raise ResidentConflict("Stale resident revision")
            self._capacity(db)
            result = callback(db, session)
            db.execute("INSERT INTO resident_operations VALUES(?,?,?,?,?,?)",
                       (*self.key, request.session_id, request.dedupe_id, digest, encode(result)))
            return {**result, "state": self._state(db, request.session_id)}

    def undo(self, change_id: str, request: UndoRequest):
        return self._operation(request, "undo", change_id,
                               lambda db, session: {"change": self._undo_change(db, session, change_id)})

    def retract(self, event_id: str, request: RetractRequest):
        def operation(db, session):
            row = db.execute("SELECT payload_json FROM resident_feedback WHERE event_id=? AND instance_id=? AND owner_key=?", (event_id, *self.key)).fetchone()
            if row is None:
                raise ValueError("Unknown resident feedback")
            event = json.loads(row[0])
            change = None
            if event.get("change_id"):
                linked = next((c for c in self._changes(db) if c["change_id"] == event["change_id"]), None)
                if linked and (linked["scope"] != "session" or linked["session_id"] == session["session_id"]):
                    try:
                        change = self._undo_change(db, session, event["change_id"])
                    except ResidentConflict:
                        # A later same-field instruction is independent and survives deletion．
                        pass
            event.update(status="retracted", deleted=request.delete, training_consent=False,
                         retracted_at=self.clock().isoformat())
            if request.delete:
                event.update(text="", target=None, participant_scope=[], proposed_change=None)
            db.execute("UPDATE resident_feedback SET payload_json=? WHERE event_id=?", (encode(event), event_id))
            return {"event_id": event_id, "status": "retracted", "deleted": request.delete, "change": change}
        return self._operation(request, "delete" if request.delete else "retract", event_id, operation)
