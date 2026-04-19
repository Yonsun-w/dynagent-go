package memory

import (
	"context"
	"sort"
	"strings"

	"github.com/admin/ai_project/internal/persistence"
	"github.com/admin/ai_project/internal/state"
)

type Engine struct {
	store persistence.Store
}

func New(store persistence.Store) *Engine {
	return &Engine{store: store}
}

func (e *Engine) RecordStep(ctx context.Context, st *state.State, nodeID string) error {
	trajectory := append([]string(nil), e.currentSequence(st)...)
	trajectory = append(trajectory, nodeID)
	return e.store.PutShortTerm(ctx, st.Task.ID, trajectory)
}

func (e *Engine) Finalize(ctx context.Context, st *state.State) error {
	return e.store.UpsertPattern(ctx, persistence.Pattern{
		Keywords: st.UserInput.Keywords,
		Nodes:    e.currentSequence(st),
		HitCount: 1,
	})
}

func (e *Engine) RecommendNodes(ctx context.Context, st *state.State) ([]string, error) {
	patterns, err := e.store.RecallPatterns(ctx, st.UserInput.Keywords)
	if err != nil {
		return nil, err
	}
	score := map[string]int{}
	for _, pattern := range patterns {
		for _, nodeID := range pattern.Nodes {
			score[nodeID] += pattern.HitCount
		}
	}
	type scored struct {
		nodeID string
		score  int
	}
	var ranking []scored
	for nodeID, value := range score {
		ranking = append(ranking, scored{nodeID: nodeID, score: value})
	}
	sort.Slice(ranking, func(i, j int) bool {
		if ranking[i].score == ranking[j].score {
			return strings.Compare(ranking[i].nodeID, ranking[j].nodeID) < 0
		}
		return ranking[i].score > ranking[j].score
	})
	var out []string
	for _, item := range ranking {
		out = append(out, item.nodeID)
	}
	return out, nil
}

func (e *Engine) currentSequence(st *state.State) []string {
	var sequence []string
	for _, record := range st.DecisionLog {
		if record.NextNode != "__terminate__" {
			sequence = append(sequence, record.NextNode)
		}
	}
	return sequence
}
