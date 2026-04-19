package persistence

import (
	"crypto/sha1"
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/admin/ai_project/internal/config"
	"github.com/admin/ai_project/internal/state"
)

type PostgresStore struct {
	pool *pgxpool.Pool
}

func NewPostgresStore(ctx context.Context, cfg config.StorageConfig) (*PostgresStore, error) {
	pool, err := pgxpool.New(ctx, cfg.PostgresDSN)
	if err != nil {
		return nil, fmt.Errorf("connect postgres: %w", err)
	}
	return &PostgresStore{pool: pool}, nil
}

func (s *PostgresStore) Close() {
	if s.pool != nil {
		s.pool.Close()
	}
}

func (s *PostgresStore) CreateTask(ctx context.Context, st state.State) error {
	inputJSON, _ := json.Marshal(st.UserInput)
	metaJSON, _ := json.Marshal(st.Task.Labels)
	_, err := s.pool.Exec(ctx, `
		INSERT INTO tasks (id, status, trace_id, input_json, metadata_json)
		VALUES ($1, $2, $3, $4, $5)
	`, st.Task.ID, st.Task.Status, st.Trace.TraceID, inputJSON, metaJSON)
	return err
}

func (s *PostgresStore) UpdateTask(ctx context.Context, st state.State) error {
	inputJSON, _ := json.Marshal(st.UserInput)
	metaJSON, _ := json.Marshal(st.Task.Labels)
	_, err := s.pool.Exec(ctx, `
		UPDATE tasks
		SET status = $2, updated_at = NOW(), trace_id = $3, input_json = $4, metadata_json = $5
		WHERE id = $1
	`, st.Task.ID, st.Task.Status, st.Trace.TraceID, inputJSON, metaJSON)
	return err
}

func (s *PostgresStore) SaveStep(ctx context.Context, taskID string, step StepRecord) error {
	resultJSON, _ := json.Marshal(step.Output)
	_, err := s.pool.Exec(ctx, `
		INSERT INTO task_steps (task_id, step_index, node_id, status, started_at, finished_at, reasoning, result_json)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (task_id, step_index) DO UPDATE SET
			node_id = EXCLUDED.node_id,
			status = EXCLUDED.status,
			started_at = EXCLUDED.started_at,
			finished_at = EXCLUDED.finished_at,
			reasoning = EXCLUDED.reasoning,
			result_json = EXCLUDED.result_json
	`, taskID, step.StepIndex, step.NodeID, step.Status, step.StartedAt, step.FinishedAt, step.Reasoning, resultJSON)
	if err != nil {
		return err
	}
	inputJSON, _ := json.Marshal(step.Input)
	outputJSON, _ := json.Marshal(step.Output)
	_, err = s.pool.Exec(ctx, `
		INSERT INTO node_lineage (task_id, step_index, node_id, input_json, output_json, started_at, finished_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (task_id, step_index) DO UPDATE SET
			node_id = EXCLUDED.node_id,
			input_json = EXCLUDED.input_json,
			output_json = EXCLUDED.output_json,
			started_at = EXCLUDED.started_at,
			finished_at = EXCLUDED.finished_at
	`, taskID, step.StepIndex, step.NodeID, inputJSON, outputJSON, step.StartedAt, step.FinishedAt)
	return err
}

func (s *PostgresStore) SaveSnapshot(ctx context.Context, snapshot state.Snapshot) error {
	patchJSON, _ := json.Marshal(snapshot.Patch)
	stateJSON, _ := json.Marshal(snapshot.State)
	_, err := s.pool.Exec(ctx, `
		INSERT INTO state_snapshots (task_id, version, patch_json, state_json)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (task_id, version) DO UPDATE SET
			patch_json = EXCLUDED.patch_json,
			state_json = EXCLUDED.state_json
	`, snapshot.TaskID, snapshot.Version, patchJSON, stateJSON)
	return err
}

func (s *PostgresStore) SaveSummary(ctx context.Context, taskID string, summary map[string]any) error {
	summaryJSON, _ := json.Marshal(summary)
	keywords := []string{}
	if raw, ok := summary["keywords"].([]string); ok {
		keywords = raw
	}
	sequence := []string{}
	if raw, ok := summary["node_sequence"].([]string); ok {
		sequence = raw
	}
	status := fmt.Sprint(summary["status"])
	_, err := s.pool.Exec(ctx, `
		INSERT INTO task_summaries (task_id, status, keywords, node_sequence, summary_json)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (task_id) DO UPDATE SET
			status = EXCLUDED.status,
			keywords = EXCLUDED.keywords,
			node_sequence = EXCLUDED.node_sequence,
			summary_json = EXCLUDED.summary_json
	`, taskID, status, keywords, sequence, summaryJSON)
	return err
}

func (s *PostgresStore) GetTask(ctx context.Context, taskID string) (TaskRecord, error) {
	var (
		status     string
		traceID    string
		inputJSON  []byte
		labelsJSON []byte
	)
	if err := s.pool.QueryRow(ctx, `SELECT status, trace_id, input_json, metadata_json FROM tasks WHERE id = $1`, taskID).
		Scan(&status, &traceID, &inputJSON, &labelsJSON); err != nil {
		return TaskRecord{}, err
	}
	var input state.UserInput
	var labels map[string]string
	_ = json.Unmarshal(inputJSON, &input)
	_ = json.Unmarshal(labelsJSON, &labels)
	record := TaskRecord{
		State: state.State{
			Task: state.TaskMeta{ID: taskID, Status: state.TaskStatus(status), Labels: labels},
			UserInput: input,
			Trace: state.Trace{TraceID: traceID},
			WorkingMemory: map[string]any{},
			NodeOutputs: map[string]map[string]any{},
			Sensitive: map[string]string{},
			Ext: map[string]any{},
		},
	}
	rows, err := s.pool.Query(ctx, `
		SELECT step_index, node_id, status, reasoning, started_at, finished_at, result_json
		FROM task_steps WHERE task_id = $1 ORDER BY step_index ASC
	`, taskID)
	if err != nil {
		return TaskRecord{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var step StepRecord
		var outputJSON []byte
		if err := rows.Scan(&step.StepIndex, &step.NodeID, &step.Status, &step.Reasoning, &step.StartedAt, &step.FinishedAt, &outputJSON); err != nil {
			return TaskRecord{}, err
		}
		_ = json.Unmarshal(outputJSON, &step.Output)
		record.Steps = append(record.Steps, step)
	}
	snapshotRows, err := s.pool.Query(ctx, `
		SELECT version, patch_json, state_json, created_at
		FROM state_snapshots WHERE task_id = $1 ORDER BY version ASC
	`, taskID)
	if err != nil {
		return TaskRecord{}, err
	}
	defer snapshotRows.Close()
	for snapshotRows.Next() {
		var snapshot state.Snapshot
		var patchJSON []byte
		var stateJSON []byte
		if err := snapshotRows.Scan(&snapshot.Version, &patchJSON, &stateJSON, &snapshot.CreatedAt); err != nil {
			return TaskRecord{}, err
		}
		snapshot.TaskID = taskID
		_ = json.Unmarshal(patchJSON, &snapshot.Patch)
		_ = json.Unmarshal(stateJSON, &snapshot.State)
		record.Snapshots = append(record.Snapshots, snapshot)
	}
	var summaryJSON []byte
	if err := s.pool.QueryRow(ctx, `SELECT summary_json FROM task_summaries WHERE task_id = $1`, taskID).Scan(&summaryJSON); err == nil {
		_ = json.Unmarshal(summaryJSON, &record.Summary)
	}
	return record, nil
}

func (s *PostgresStore) GetLatestSnapshot(ctx context.Context, taskID string) (state.Snapshot, error) {
	var snapshot state.Snapshot
	var patchJSON []byte
	var stateJSON []byte
	if err := s.pool.QueryRow(ctx, `
		SELECT version, patch_json, state_json, created_at
		FROM state_snapshots WHERE task_id = $1 ORDER BY version DESC LIMIT 1
	`, taskID).Scan(&snapshot.Version, &patchJSON, &stateJSON, &snapshot.CreatedAt); err != nil {
		return state.Snapshot{}, err
	}
	snapshot.TaskID = taskID
	_ = json.Unmarshal(patchJSON, &snapshot.Patch)
	_ = json.Unmarshal(stateJSON, &snapshot.State)
	return snapshot, nil
}

func (s *PostgresStore) PutShortTerm(ctx context.Context, taskID string, nodes []string) error {
	_ = ctx
	_ = taskID
	_ = nodes
	return nil
}

func (s *PostgresStore) UpsertPattern(ctx context.Context, pattern Pattern) error {
	key, err := patternID(pattern)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO memory_patterns (pattern_id, keywords, node_sequence, hit_count)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (pattern_id) DO UPDATE SET
			hit_count = memory_patterns.hit_count + EXCLUDED.hit_count,
			updated_at = NOW()
	`, key, pattern.Keywords, pattern.Nodes, pattern.HitCount)
	return err
}

func (s *PostgresStore) RecallPatterns(ctx context.Context, keywords []string) ([]Pattern, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT keywords, node_sequence, hit_count
		FROM memory_patterns
		WHERE keywords && $1
		ORDER BY hit_count DESC
		LIMIT 20
	`, keywords)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var patterns []Pattern
	for rows.Next() {
		var pattern Pattern
		if err := rows.Scan(&pattern.Keywords, &pattern.Nodes, &pattern.HitCount); err != nil {
			return nil, err
		}
		patterns = append(patterns, pattern)
	}
	return patterns, nil
}

func patternID(pattern Pattern) (string, error) {
	raw, err := json.Marshal(pattern)
	if err != nil {
		return "", fmt.Errorf("marshal pattern id: %w", err)
	}
	sum := sha1.Sum(raw)
	return fmt.Sprintf("%x", sum[:]), nil
}
