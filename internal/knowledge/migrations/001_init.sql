-- App Knowledge (Phase 5): service map, baselines, documents with embeddings, incidents.
CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE IF NOT EXISTS services (
    namespace   text NOT NULL,
    name        text NOT NULL,
    kind        text NOT NULL,          -- app, datastore, cache, infra
    image       text NOT NULL DEFAULT '',
    replicas    int  NOT NULL DEFAULT 0,
    ports       jsonb NOT NULL DEFAULT '[]',
    learned_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (namespace, name)
);

-- "from depends on to" (from calls to), with how we know it.
CREATE TABLE IF NOT EXISTS edges (
    namespace   text NOT NULL,
    from_svc    text NOT NULL,
    to_svc      text NOT NULL,
    source      text NOT NULL,          -- trace, env, name
    calls       bigint NOT NULL DEFAULT 0,
    learned_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (namespace, from_svc, to_svc, source)
);

-- Normal behaviour, measured over a healthy window.
CREATE TABLE IF NOT EXISTS baselines (
    namespace    text NOT NULL,
    service      text NOT NULL,
    metric       text NOT NULL,         -- e.g. endpoint:hotels:error_ratio, memory_mib
    value        double precision NOT NULL,
    window_text  text NOT NULL,
    measured_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (namespace, service, metric)
);

-- Runbooks, READMEs and resolved incidents, chunked and embedded (nomic-embed-text: 768 dims).
CREATE TABLE IF NOT EXISTS documents (
    id          bigserial PRIMARY KEY,
    kind        text NOT NULL,          -- runbook, readme, incident
    source      text NOT NULL,          -- file path or incident id
    title       text NOT NULL,
    chunk       text NOT NULL,
    hash        text NOT NULL UNIQUE,   -- sha256(source + chunk): re-ingest is idempotent
    embedding   vector(768) NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS documents_embedding_idx ON documents USING hnsw (embedding vector_cosine_ops);

-- Investigations and their outcome. The Safety Gate (Phase 6) marks them resolved.
CREATE TABLE IF NOT EXISTS incidents (
    id          bigserial PRIMARY KEY,
    namespace   text NOT NULL,
    target      text NOT NULL,
    source      text NOT NULL DEFAULT 'cli',   -- cli, alert
    summary     text NOT NULL DEFAULT '',
    answer      jsonb NOT NULL DEFAULT '{}',
    status      text NOT NULL DEFAULT 'open',  -- open, proposed, approved, rejected, applied, rolled_back, resolved
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);
