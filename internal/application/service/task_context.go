package service

import (
	"context"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/hibiken/asynq"
)

// Use Asynq metadata in Redis deployments and the equivalent persisted local
// executor metadata in portable deployments.
func backgroundTaskRetryCount(ctx context.Context) (int, bool) {
	if n, ok := asynq.GetRetryCount(ctx); ok {
		return n, true
	}
	n, _, ok := types.TaskRetryMetadataFromContext(ctx)
	return n, ok
}
func backgroundTaskMaxRetry(ctx context.Context) (int, bool) {
	if n, ok := asynq.GetMaxRetry(ctx); ok {
		return n, true
	}
	_, n, ok := types.TaskRetryMetadataFromContext(ctx)
	return n, ok
}
func backgroundTaskID(ctx context.Context) (string, bool) {
	if id, ok := asynq.GetTaskID(ctx); ok {
		return id, true
	}
	return types.BackgroundTaskIDFromContext(ctx)
}
