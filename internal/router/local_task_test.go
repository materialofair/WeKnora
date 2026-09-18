package router

import (
	"context"
	"errors"
	"github.com/hibiken/asynq"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func localTestExecutor(t *testing.T) *SyncTaskExecutor {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "tasks.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	e, err := NewDurableTaskExecutor(db)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.local.Close(); sql, _ := db.DB(); sql.Close() })
	return e
}
func waitLocal(t *testing.T, fn func() bool) {
	t.Helper()
	until := time.Now().Add(4 * time.Second)
	for time.Now().Before(until) {
		if fn() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("task did not reach expected state")
}
func TestLocalTasksDelayRetryAndDuplicate(t *testing.T) {
	e := localTestExecutor(t)
	var attempts atomic.Int32
	e.RegisterHandler("test", func(context.Context, *asynq.Task) error {
		if attempts.Add(1) == 1 {
			return errors.New("retry")
		}
		return nil
	})
	e.local.retryDelay = 10 * time.Millisecond
	info, err := e.Enqueue(asynq.NewTask("test", nil), asynq.TaskID("same"), asynq.ProcessIn(150*time.Millisecond), asynq.MaxRetry(1), asynq.Retention(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.Enqueue(asynq.NewTask("test", nil), asynq.TaskID("same")); !errors.Is(err, asynq.ErrTaskIDConflict) {
		t.Fatalf("duplicate: %v", err)
	}
	if attempts.Load() != 0 {
		t.Fatal("started before handlers ready")
	}
	if err = e.local.Start(e); err != nil {
		t.Fatal(err)
	}
	time.Sleep(40 * time.Millisecond)
	if attempts.Load() != 0 {
		t.Fatal("ran before schedule")
	}
	waitLocal(t, func() bool {
		var row localTask
		e.local.db.First(&row, "id = ?", info.ID)
		return row.State == "completed"
	})
	if attempts.Load() != 2 {
		t.Fatal("retry missing")
	}
}
func TestLocalRecoveryAndCancellation(t *testing.T) {
	e := localTestExecutor(t)
	var attempts atomic.Int32
	e.RegisterHandler("test", func(context.Context, *asynq.Task) error { attempts.Add(1); return nil })
	info, err := e.Enqueue(asynq.NewTask("test", nil))
	if err != nil {
		t.Fatal(err)
	}
	e.local.db.Model(&localTask{}).Where("id = ?", info.ID).Update("state", "active")
	if err = e.local.Start(e); err != nil {
		t.Fatal(err)
	}
	waitLocal(t, func() bool { return attempts.Load() == 1 })
	e.RegisterHandler("document:process", func(context.Context, *asynq.Task) error { t.Error("cancelled task ran"); return nil })
	_, err = e.Enqueue(asynq.NewTask("document:process", []byte(`{"knowledge_id":"k1"}`)), asynq.ProcessIn(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	n, _, err := e.local.CancelTasksForKnowledge(context.Background(), "k1")
	if err != nil || n != 1 {
		t.Fatalf("cancel %d %v", n, err)
	}
	found, err := e.local.HasQueuedTasksForKnowledge(context.Background(), "k1")
	if err != nil || found {
		t.Fatalf("still queued %v", err)
	}
}
func TestLocalConcurrencyAndShutdownRecovery(t *testing.T) {
	e := localTestExecutor(t)
	var running, peak atomic.Int32
	e.RegisterHandler("blocked", func(ctx context.Context, _ *asynq.Task) error {
		n := running.Add(1)
		defer running.Add(-1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		<-ctx.Done()
		return ctx.Err()
	})
	for range 10 {
		if _, err := e.Enqueue(asynq.NewTask("blocked", nil)); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.local.Start(e); err != nil {
		t.Fatal(err)
	}
	waitLocal(t, func() bool { return running.Load() == 4 })
	if err := e.local.Close(); err != nil {
		t.Fatal(err)
	}
	if peak.Load() != 4 {
		t.Fatalf("peak %d", peak.Load())
	}
	var count int64
	e.local.db.Model(&localTask{}).Where("state = ?", "pending").Count(&count)
	if count != 10 {
		t.Fatalf("shutdown lost work: %d", count)
	}
	// Reopen executor against the same database, as a new process would.
	resumed, err := NewDurableTaskExecutor(e.local.db)
	if err != nil {
		t.Fatal(err)
	}
	defer resumed.Close()
	var completed atomic.Int32
	resumed.RegisterHandler("blocked", func(context.Context, *asynq.Task) error { completed.Add(1); return nil })
	if err = resumed.local.Start(resumed); err != nil {
		t.Fatal(err)
	}
	waitLocal(t, func() bool { return completed.Load() == 10 })
}
func TestLocalTaskIDReusableAfterSuccessAndRuntimeActions(t *testing.T) {
	e := localTestExecutor(t)
	e.RegisterHandler("done", func(context.Context, *asynq.Task) error { return nil })
	if err := e.local.Start(e); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Enqueue(asynq.NewTask("done", nil), asynq.TaskID("repeat")); err != nil {
		t.Fatal(err)
	}
	waitLocal(t, func() bool {
		var n int64
		e.local.db.Model(&localTask{}).Where("id = ?", "repeat").Count(&n)
		return n == 0
	})
	if _, err := e.Enqueue(asynq.NewTask("done", nil), asynq.TaskID("repeat"), asynq.ProcessIn(time.Hour)); err != nil {
		t.Fatal(err)
	}
	page, supported, err := e.local.ListRuntimeTasks(context.Background(), "default", "scheduled", "", 10)
	if err != nil || !supported || len(page.Tasks) != 1 {
		t.Fatalf("runtime list: %+v %v", page, err)
	}
	if _, err = e.local.RunRuntimeTask(context.Background(), "default", "repeat"); err != nil {
		t.Fatal(err)
	}
	waitLocal(t, func() bool {
		var n int64
		e.local.db.Model(&localTask{}).Where("id = ?", "repeat").Count(&n)
		return n == 0
	})
}
func TestLocalOutcomeWriteRetriesWithoutExecutingAgain(t *testing.T) {
	e := localTestExecutor(t)
	row := localTask{ID: "write-retry", Type: "test", Queue: "default", State: "active", Retention: time.Hour}
	if err := e.local.db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	var writes atomic.Int32
	if err := e.local.db.Callback().Update().Before("gorm:update").Register("test:transient-failure", func(db *gorm.DB) {
		if writes.Add(1) == 1 {
			db.AddError(errors.New("transient storage failure"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	defer e.local.db.Callback().Update().Remove("test:transient-failure")
	e.local.finish(row, nil)
	var saved localTask
	if err := e.local.db.First(&saved, "id = ?", row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if writes.Load() != 2 || saved.State != "completed" {
		t.Fatalf("outcome not retried: writes=%d state=%s", writes.Load(), saved.State)
	}
}
