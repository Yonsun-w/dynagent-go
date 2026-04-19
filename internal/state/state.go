package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

type TaskStatus string

const (
	TaskStatusPending   TaskStatus = "pending"
	TaskStatusRunning   TaskStatus = "running"
	TaskStatusCompleted TaskStatus = "completed"
	TaskStatusFailed    TaskStatus = "failed"
	TaskStatusTimedOut  TaskStatus = "timed_out"
)

type TaskMeta struct {
	ID        string            `json:"id"`
	Status    TaskStatus        `json:"status"`
	CreatedAt time.Time         `json:"created_at"`
	UpdatedAt time.Time         `json:"updated_at"`
	Labels    map[string]string `json:"labels"`
}

type UserInput struct {
	Text     string         `json:"text"`
	Keywords []string       `json:"keywords"`
	Ext      map[string]any `json:"ext"`
}

type Trace struct {
	TraceID string `json:"trace_id"`
}

type DecisionRecord struct {
	DecisionType string         `json:"decision_type"`
	FunctionName string         `json:"function_name"`
	FunctionArgs map[string]any `json:"function_args,omitempty"`
	Step         int            `json:"step"`
	NextNode     string         `json:"next_node"`
	Reasoning    string         `json:"reasoning"`
	Data         map[string]any `json:"data"`
	At           time.Time      `json:"at"`
}

type State struct {
	Version       int64                     `json:"version"`
	Task          TaskMeta                  `json:"task"`
	UserInput     UserInput                 `json:"user_input"`
	WorkingMemory map[string]any            `json:"working_memory"`
	NodeOutputs   map[string]map[string]any `json:"node_outputs"`
	DecisionLog   []DecisionRecord          `json:"decision_log"`
	Trace         Trace                     `json:"trace"`
	Sensitive     map[string]string         `json:"sensitive"`
	Ext           map[string]any            `json:"ext"`
}

type Patch struct {
	WorkingMemory map[string]any            `json:"working_memory,omitempty"`
	NodeOutputs   map[string]map[string]any `json:"node_outputs,omitempty"`
	Sensitive     map[string]string         `json:"sensitive,omitempty"`
	Ext           map[string]any            `json:"ext,omitempty"`
}

type Snapshot struct {
	TaskID    string    `json:"task_id"`
	Version   int64     `json:"version"`
	CreatedAt time.Time `json:"created_at"`
	Patch     Patch     `json:"patch"`
	State     State     `json:"state"`
}

type ReadOnlyState struct {
	snapshot State
}

func New(taskID string, traceID string, input UserInput, labels map[string]string) (*State, error) {
	if taskID == "" {
		return nil, errors.New("task id is required")
	}
	now := time.Now().UTC()
	if labels == nil {
		labels = map[string]string{}
	}
	st := &State{
		Version: 1,
		Task: TaskMeta{
			ID:        taskID,
			Status:    TaskStatusPending,
			CreatedAt: now,
			UpdatedAt: now,
			Labels:    labels,
		},
		UserInput:     input,
		WorkingMemory: map[string]any{},
		NodeOutputs:   map[string]map[string]any{},
		DecisionLog:   []DecisionRecord{},
		Trace:         Trace{TraceID: traceID},
		Sensitive:     map[string]string{},
		Ext:           map[string]any{},
	}
	return st, st.Validate()
}

func (s *State) Validate() error {
	if s == nil {
		return errors.New("state is nil")
	}
	if s.Task.ID == "" {
		return errors.New("task.id is required")
	}
	if s.Trace.TraceID == "" {
		return errors.New("trace.trace_id is required")
	}
	if s.WorkingMemory == nil {
		return errors.New("working_memory must not be nil")
	}
	if s.NodeOutputs == nil {
		return errors.New("node_outputs must not be nil")
	}
	if s.Sensitive == nil {
		return errors.New("sensitive must not be nil")
	}
	if s.Ext == nil {
		return errors.New("ext must not be nil")
	}
	return nil
}

func (s *State) Clone() (*State, error) {
	raw, err := json.Marshal(s)
	if err != nil {
		return nil, fmt.Errorf("marshal state: %w", err)
	}
	var clone State
	if err := json.Unmarshal(raw, &clone); err != nil {
		return nil, fmt.Errorf("unmarshal state clone: %w", err)
	}
	return &clone, nil
}

func (s *State) ReadOnly() (*ReadOnlyState, error) {
	clone, err := s.Clone()
	if err != nil {
		return nil, err
	}
	return &ReadOnlyState{snapshot: *clone}, nil
}

func (r *ReadOnlyState) Snapshot() State {
	return r.snapshot
}

func (r *ReadOnlyState) ToMap() (map[string]any, error) {
	raw, err := json.Marshal(r.snapshot)
	if err != nil {
		return nil, fmt.Errorf("marshal readonly state: %w", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("unmarshal readonly state map: %w", err)
	}
	return out, nil
}

func (r *ReadOnlyState) UserText() string {
	return r.snapshot.UserInput.Text
}

func (s *State) ApplyPatch(nodeID string, patch Patch, sensitiveFields []string) (*Snapshot, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	before := s.Version
	if patch.WorkingMemory != nil {
		for key, value := range patch.WorkingMemory {
			s.WorkingMemory[key] = value
		}
	}
	if patch.NodeOutputs != nil {
		for key, value := range patch.NodeOutputs {
			if s.NodeOutputs[key] == nil {
				s.NodeOutputs[key] = map[string]any{}
			}
			for nestedKey, nestedValue := range value {
				s.NodeOutputs[key][nestedKey] = nestedValue
			}
		}
	}
	if patch.Sensitive != nil {
		for key, value := range patch.Sensitive {
			if slices.Contains(sensitiveFields, key) {
				s.Sensitive[key] = value
			}
		}
	}
	if patch.Ext != nil {
		for key, value := range patch.Ext {
			s.Ext[key] = value
		}
	}
	s.Version++
	s.Task.UpdatedAt = time.Now().UTC()
	s.Task.Status = TaskStatusRunning

	clone, err := s.Clone()
	if err != nil {
		return nil, err
	}

	_ = nodeID
	return &Snapshot{
		TaskID:    s.Task.ID,
		Version:   before + 1,
		CreatedAt: time.Now().UTC(),
		Patch:     patch,
		State:     *clone,
	}, nil
}

func (s *State) AppendDecision(record DecisionRecord) {
	s.DecisionLog = append(s.DecisionLog, record)
	s.Task.UpdatedAt = time.Now().UTC()
}

func LookupPath(input map[string]any, path string) (any, bool) {
	parts := strings.Split(path, ".")
	var current any = input
	for _, part := range parts {
		obj, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		next, ok := obj[part]
		if !ok {
			return nil, false
		}
		current = next
	}
	return current, true
}

func FromMap(input map[string]any) (*State, error) {
	raw, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("marshal state map: %w", err)
	}
	var out State
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("unmarshal state map: %w", err)
	}
	if out.WorkingMemory == nil {
		out.WorkingMemory = map[string]any{}
	}
	if out.NodeOutputs == nil {
		out.NodeOutputs = map[string]map[string]any{}
	}
	if out.Sensitive == nil {
		out.Sensitive = map[string]string{}
	}
	if out.Ext == nil {
		out.Ext = map[string]any{}
	}
	return &out, out.Validate()
}
