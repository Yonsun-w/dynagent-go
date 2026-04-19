package sandbox

import (
	"context"
	"fmt"
	"time"

	"golang.org/x/sync/semaphore"

	"github.com/admin/ai_project/internal/node"
	"github.com/admin/ai_project/internal/state"
)

type Executor struct {
	pool        *semaphore.Weighted
	nodeTimeout time.Duration
}

func New(maxParallel int64, nodeTimeout time.Duration) *Executor {
	return &Executor{
		pool:        semaphore.NewWeighted(maxParallel),
		nodeTimeout: nodeTimeout,
	}
}

func (e *Executor) Execute(ctx context.Context, n node.Node, st *state.ReadOnlyState) (node.Result, error) {
	if err := e.pool.Acquire(ctx, 1); err != nil {
		return node.Result{}, fmt.Errorf("acquire sandbox slot: %w", err)
	}
	defer e.pool.Release(1)

	childCtx, cancel := context.WithTimeout(ctx, e.nodeTimeout)
	defer cancel()

	resultCh := make(chan node.Result, 1)
	errCh := make(chan error, 1)
	go func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				errCh <- fmt.Errorf("node panic recovered: %v", recovered)
			}
		}()
		resultCh <- n.Execute(childCtx, st)
	}()

	select {
	case <-childCtx.Done():
		return node.Result{Success: false, Error: childCtx.Err().Error()}, childCtx.Err()
	case err := <-errCh:
		return node.Result{Success: false, Error: err.Error()}, err
	case result := <-resultCh:
		return result, nil
	}
}
