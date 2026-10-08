-- +goose Up

CREATE TABLE evaluation_test_sets (
    workspace_id text NOT NULL,
    set_id text NOT NULL,
    name text NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (workspace_id, set_id),
    CHECK (name <> '')
);

CREATE TABLE evaluation_test_cases (
    workspace_id text NOT NULL,
    case_id text NOT NULL,
    set_id text NOT NULL,
    question text NOT NULL,
    expected_source_id text NOT NULL,
    note text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (workspace_id, case_id),
    FOREIGN KEY (workspace_id, set_id) REFERENCES evaluation_test_sets (workspace_id, set_id) ON DELETE CASCADE,
    CHECK (question <> ''),
    CHECK (expected_source_id <> '')
);
CREATE INDEX evaluation_test_cases_set_idx ON evaluation_test_cases (workspace_id, set_id, created_at);

-- Runs intentionally do not reference the mutable test set. Their identifying
-- fields and result JSON are immutable snapshots that survive later edits.
CREATE TABLE evaluation_runs (
    workspace_id text NOT NULL,
    run_id text NOT NULL,
    set_id text NOT NULL,
    set_name text NOT NULL,
    top_k integer NOT NULL,
    threshold double precision NOT NULL,
    total_count integer NOT NULL,
    evaluable_count integer NOT NULL,
    passed_count integer NOT NULL,
    source_missing_count integer NOT NULL,
    pass_rate double precision NOT NULL,
    results jsonb NOT NULL,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (workspace_id, run_id),
    CHECK (top_k BETWEEN 1 AND 20),
    CHECK (threshold BETWEEN 0 AND 1),
    CHECK (total_count >= 0),
    CHECK (evaluable_count BETWEEN 0 AND total_count),
    CHECK (passed_count BETWEEN 0 AND evaluable_count),
    CHECK (source_missing_count >= 0),
    CHECK (evaluable_count + source_missing_count = total_count),
    CHECK (pass_rate BETWEEN 0 AND 1)
);
CREATE INDEX evaluation_runs_set_idx ON evaluation_runs (workspace_id, set_id, created_at DESC);

-- +goose Down

DROP TABLE evaluation_runs;
DROP TABLE evaluation_test_cases;
DROP TABLE evaluation_test_sets;
