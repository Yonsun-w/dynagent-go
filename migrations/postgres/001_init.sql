CREATE TABLE IF NOT EXISTS tasks (
    id TEXT PRIMARY KEY,
    status TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    trace_id TEXT NOT NULL,
    input_json JSONB NOT NULL,
    metadata_json JSONB NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_tasks_status_created_at ON tasks(status, created_at DESC);

CREATE TABLE IF NOT EXISTS task_steps (
    task_id TEXT NOT NULL,
    step_index INT NOT NULL,
    node_id TEXT NOT NULL,
    status TEXT NOT NULL,
    started_at TIMESTAMPTZ NOT NULL,
    finished_at TIMESTAMPTZ NOT NULL,
    reasoning TEXT NOT NULL,
    result_json JSONB NOT NULL,
    PRIMARY KEY(task_id, step_index)
);

CREATE INDEX IF NOT EXISTS idx_task_steps_node_id ON task_steps(node_id);

CREATE TABLE IF NOT EXISTS state_snapshots (
    task_id TEXT NOT NULL,
    version BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    patch_json JSONB NOT NULL,
    state_json JSONB NOT NULL,
    PRIMARY KEY(task_id, version)
);

CREATE TABLE IF NOT EXISTS task_summaries (
    task_id TEXT PRIMARY KEY,
    status TEXT NOT NULL,
    keywords TEXT[] NOT NULL,
    node_sequence TEXT[] NOT NULL,
    summary_json JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_task_summaries_keywords ON task_summaries USING GIN(keywords);

CREATE TABLE IF NOT EXISTS node_lineage (
    task_id TEXT NOT NULL,
    step_index INT NOT NULL,
    node_id TEXT NOT NULL,
    input_json JSONB NOT NULL,
    output_json JSONB NOT NULL,
    started_at TIMESTAMPTZ NOT NULL,
    finished_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY(task_id, step_index)
);

CREATE TABLE IF NOT EXISTS memory_patterns (
    pattern_id TEXT PRIMARY KEY,
    keywords TEXT[] NOT NULL,
    node_sequence TEXT[] NOT NULL,
    hit_count BIGINT NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS ai_call_logs (
    trace_id TEXT PRIMARY KEY,
    provider TEXT NOT NULL,
    model TEXT NOT NULL,
    request_json JSONB NOT NULL,
    response_json JSONB NOT NULL,
    status_code INT NOT NULL,
    duration_ms BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
