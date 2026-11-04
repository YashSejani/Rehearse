-- Safety Gate (Phase 6): one row per proposed action, updated as it moves through the
-- pipeline, plus an append-only event log. Together they are the audit trail.
CREATE TABLE IF NOT EXISTS proposals (
    id           bigserial PRIMARY KEY,
    incident_id  bigint,
    namespace    text NOT NULL,
    deployment   text NOT NULL,
    action       jsonb NOT NULL,
    answer       jsonb NOT NULL DEFAULT '{}',   -- the agent's diagnosis (evidence, confidence)
    plan         jsonb,                         -- patch + inverse
    diff         text NOT NULL DEFAULT '',
    rehearsal    text NOT NULL DEFAULT '',      -- "rehearsed: healthy in 41s" / "not rehearsed (...)"
    status       text NOT NULL,                 -- blocked, awaiting_approval, rejected, applying, watching, applied, rolled_back, failed
    reason       text NOT NULL DEFAULT '',
    approved_by  text NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS audit_events (
    id           bigserial PRIMARY KEY,
    proposal_id  bigint NOT NULL REFERENCES proposals(id),
    at           timestamptz NOT NULL DEFAULT now(),
    step         text NOT NULL,                 -- validate, dry-run, policy, rehearsal, approval, apply, watch, undo
    ok           boolean NOT NULL,
    detail       text NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS audit_events_proposal_idx ON audit_events(proposal_id);
