package service

import (
	"context"
	"github.com/Tencent/WeKnora/internal/types"
	"testing"
)

func TestPortableTaskMetadata(t *testing.T) {
	ctx := types.WithBackgroundTaskID(types.WithTaskRetryMetadata(context.Background(), 2, 5), "persisted-id")
	if n, ok := backgroundTaskRetryCount(ctx); !ok || n != 2 {
		t.Fatal("retry metadata missing")
	}
	if n, ok := backgroundTaskMaxRetry(ctx); !ok || n != 5 {
		t.Fatal("max retry metadata missing")
	}
	if id, ok := backgroundTaskID(ctx); !ok || id != "persisted-id" {
		t.Fatal("task ID missing")
	}
	if _, ok := backgroundTaskRetryCount(context.Background()); ok {
		t.Fatal("invented retry metadata")
	}
}
