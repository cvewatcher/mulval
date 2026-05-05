-- migrations/000001_initial_schema.up.sql

CREATE TABLE analyses (
    -- AIP-151 LRO name: "operations/{uuid}".
    -- Also serves as the analysis resource name: "analyses/{uuid}".
    operation_name TEXT        NOT NULL,
 
    -- SHA-256 of (edb_facts || NUL || idb_rules). Used for cache lookup.
    input_hash     TEXT        NOT NULL,
 
    -- Inputs stored verbatim for auditability and cache lookup.
    -- Newline-joined; idb_rules is empty string when no extra rules provided.
    edb_facts      TEXT        NOT NULL,
    idb_rules      TEXT        NOT NULL DEFAULT '',
 
    -- Lifecycle.
    -- Allowed values: RUNNING | SUCCEEDED | FAILED | CANCELLED
    -- Rows are inserted directly as RUNNING — no PENDING state.
    state          TEXT        NOT NULL DEFAULT 'RUNNING',
    create_time    TIMESTAMPTZ NOT NULL DEFAULT now(),
    end_time       TIMESTAMPTZ,
    error          TEXT,
 
    -- Raw MulVAL outputs — NULL until state = SUCCEEDED.
    vertices_csv   TEXT,
    arcs_csv       TEXT,
    summary        TEXT,
 
    CONSTRAINT analyses_pkey PRIMARY KEY (operation_name),
 
    -- State must be one of the known lifecycle values.
    CONSTRAINT analyses_state_check
        CHECK (state IN ('RUNNING','SUCCEEDED','FAILED','CANCELLED')),
 
    -- end_time must be set when the analysis is in a terminal state.
    CONSTRAINT analyses_end_time_check
        CHECK (
            (state IN ('SUCCEEDED','FAILED','CANCELLED') AND end_time IS NOT NULL)
            OR
            (state = 'RUNNING' AND end_time IS NULL)
        ),
 
    -- Output columns must be populated together on success.
    CONSTRAINT analyses_output_consistency_check
        CHECK (
            (state = 'SUCCEEDED' AND vertices_csv IS NOT NULL AND arcs_csv IS NOT NULL)
            OR
            (state <> 'SUCCEEDED')
        )
);
 
-- Cache lookup: identical inputs that already succeeded reuse the same result.
-- Partial so that failed/cancelled runs with the same hash can be retried.
CREATE UNIQUE INDEX analyses_input_hash_succeeded
    ON analyses (input_hash)
    WHERE state = 'SUCCEEDED';
 
-- Stable cursor pagination and time-based ordering for ListAnalyses.
-- Downstream services use this for drift detection on new MulVAL inputs.
-- DESC on create_time returns newest-first; operation_name breaks ties
-- deterministically when two rows share the same timestamp.
CREATE INDEX analyses_create_time_operation_name
    ON analyses (create_time DESC, operation_name);
