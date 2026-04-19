package resume

import (
	"context"
	"fmt"

	"github.com/admin/ai_project/internal/persistence"
	"github.com/admin/ai_project/internal/state"
)

type Service struct {
	store persistence.Store
}

func New(store persistence.Store) *Service {
	return &Service{store: store}
}

func (s *Service) LatestSnapshot(ctx context.Context, taskID string) (*state.State, error) {
	snapshot, err := s.store.GetLatestSnapshot(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("latest snapshot for %s: %w", taskID, err)
	}
	clone, err := snapshot.State.Clone()
	if err != nil {
		return nil, err
	}
	return clone, nil
}
