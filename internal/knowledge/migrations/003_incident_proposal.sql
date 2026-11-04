-- Phase 10: link an incident to the gate proposal made for it, and keep the agent result.
ALTER TABLE incidents ADD COLUMN IF NOT EXISTS proposal_id bigint;
ALTER TABLE incidents ADD COLUMN IF NOT EXISTS alert text NOT NULL DEFAULT '';
ALTER TABLE incidents ADD COLUMN IF NOT EXISTS result jsonb NOT NULL DEFAULT '{}';
