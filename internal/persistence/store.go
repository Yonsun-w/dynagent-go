package persistence

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/admin/ai_project/internal/state"
)

type Pattern struct {
	Keywords []string `json:"keywords"`
	Nodes    []string `json:"nodes"`
	HitCount int      `json:"hit_count"`
}

type TaskRecord struct {
	State    state.State         `json:"state"`
	Steps    []StepRecord        `json:"steps"`
	Summary  map[string]any      `json:"summary"`
	Snapshots []state.Snapshot   `json:"snapshots"`
}

type StepRecord struct {
	StepIndex  int               `json:"step_index"`
	NodeID     string            `json:"node_id"`
	Status     string            `json:"status"`
	Reasoning  string            `json:"reasoning"`
	StartedAt  time.Time         `json:"started_at"`
	FinishedAt time.Time         `json:"finished_at"`
	Input      map[string]any    `json:"input"`
	Output     map[string]any    `json:"output"`
}

type Store interface {
	CreateTask(ctx context.Context, st state.State) error
	UpdateTask(ctx context.Context, st state.State) error
	SaveStep(ctx context.Context, taskID string, step StepRecord) error
	SaveSnapshot(ctx context.Context, snapshot state.Snapshot) error
	SaveSummary(ctx context.Context, taskID string, summary map[string]any) error
	GetTask(ctx context.Context, taskID string) (TaskRecord, error)
	GetLatestSnapshot(ctx context.Context, taskID string) (state.Snapshot, error)
	PutShortTerm(ctx context.Context, taskID string, nodes []string) error
	UpsertPattern(ctx context.Context, pattern Pattern) error
	RecallPatterns(ctx context.Context, keywords []string) ([]Pattern, error)
}

type MemoryStore struct {
	mu        sync.RWMutex
	tasks     map[string]TaskRecord
	shortTerm map[string][]string
	patterns  []Pattern
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		tasks:     map[string]TaskRecord{},
		shortTerm: map[string][]string{},
		patterns:  []Pattern{},
	}
}

func (s *MemoryStore) CreateTask(ctx context.Context, st state.State) error {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.tasks[st.Task.ID]; exists {
		return fmt.Errorf("task %s already exists", st.Task.ID)
	}
	s.tasks[st.Task.ID] = TaskRecord{State: st}
	return nil
}

func (s *MemoryStore) UpdateTask(ctx context.Context, st state.State) error {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.tasks[st.Task.ID]
	if !ok {
		return fmt.Errorf("task %s not found", st.Task.ID)
	}
	record.State = st
	s.tasks[st.Task.ID] = record
	return nil
}

func (s *MemoryStore) SaveStep(ctx context.Context, taskID string, step StepRecord) error {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.tasks[taskID]
	if !ok {
		return fmt.Errorf("task %s not found", taskID)
	}
	record.Steps = append(record.Steps, step)
	s.tasks[taskID] = record
	return nil
}

func (s *MemoryStore) SaveSnapshot(ctx context.Context, snapshot state.Snapshot) error {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.tasks[snapshot.TaskID]
	if !ok {
		return fmt.Errorf("task %s not found", snapshot.TaskID)
	}
	record.Snapshots = append(record.Snapshots, snapshot)
	s.tasks[snapshot.TaskID] = record
	return nil
}

func (s *MemoryStore) SaveSummary(ctx context.Context, taskID string, summary map[string]any) error {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.tasks[taskID]
	if !ok {
		return fmt.Errorf("task %s not found", taskID)
	}
	record.Summary = summary
	s.tasks[taskID] = record
	return nil
}

func (s *MemoryStore) GetTask(ctx context.Context, taskID string) (TaskRecord, error) {
	_ = ctx
	s.mu.RLock()
	defer s.mu.RUnlock()
	record, ok := s.tasks[taskID]
	if !ok {
		return TaskRecord{}, fmt.Errorf("task %s not found", taskID)
	}
	return record, nil
}

func (s *MemoryStore) GetLatestSnapshot(ctx context.Context, taskID string) (state.Snapshot, error) {
	_ = ctx
	s.mu.RLock()
	defer s.mu.RUnlock()
	record, ok := s.tasks[taskID]
	if !ok {
		return state.Snapshot{}, fmt.Errorf("task %s not found", taskID)
	}
	if len(record.Snapshots) == 0 {
		return state.Snapshot{}, errors.New("no snapshots found")
	}
	return record.Snapshots[len(record.Snapshots)-1], nil
}

func (s *MemoryStore) PutShortTerm(ctx context.Context, taskID string, nodes []string) error {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	s.shortTerm[taskID] = append([]string(nil), nodes...)
	return nil
}

func (s *MemoryStore) UpsertPattern(ctx context.Context, pattern Pattern) error {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	for idx, existing := range s.patterns {
		if samePattern(existing, pattern) {
			s.patterns[idx].HitCount += pattern.HitCount
			return nil
		}
	}
	s.patterns = append(s.patterns, pattern)
	return nil
}

func (s *MemoryStore) RecallPatterns(ctx context.Context, keywords []string) ([]Pattern, error) {
	_ = ctx
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(keywords) == 0 {
		return append([]Pattern(nil), s.patterns...), nil
	}
	var out []Pattern
	for _, pattern := range s.patterns {
		if overlaps(pattern.Keywords, keywords) {
			out = append(out, pattern)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].HitCount > out[j].HitCount
	})
	return out, nil
}

func samePattern(left Pattern, right Pattern) bool {
	if len(left.Nodes) != len(right.Nodes) {
		return false
	}
	for idx := range left.Nodes {
		if left.Nodes[idx] != right.Nodes[idx] {
			return false
		}
	}
	return overlaps(left.Keywords, right.Keywords)
}

func overlaps(left []string, right []string) bool {
	set := map[string]struct{}{}
	for _, item := range left {
		set[item] = struct{}{}
	}
	for _, item := range right {
		if _, ok := set[item]; ok {
			return true
		}
	}
	return false
}
