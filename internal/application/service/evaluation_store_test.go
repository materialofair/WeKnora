package service

import (
	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"path/filepath"
	"sync"
	"testing"
)

func evaluationTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "evaluation.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sql, _ := db.DB(); sql.Close() })
	return db
}
func TestEvaluationStoreRestartAndTenantIsolation(t *testing.T) {
	db := evaluationTestDB(t)
	store, err := newEvaluationDBStorage(db)
	if err != nil {
		t.Fatal(err)
	}
	for _, status := range []types.EvaluationStatue{0, 1, 2, 3} {
		d := &types.EvaluationDetail{Task: &types.EvaluationTask{ID: string(rune('a' + status)), TenantID: 7, Status: status, Total: 10, Finished: 3}, Params: &types.ChatManage{}, Metric: &types.MetricResult{RetrievalMetrics: types.RetrievalMetrics{Precision: 0.75}}}
		if err = store.register(d); err != nil {
			t.Fatal(err)
		}
	}
	reopened, err := newEvaluationDBStorage(db)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "b", "c", "d"} {
		d, err := reopened.get(7, id)
		if err != nil {
			t.Fatal(err)
		}
		if d.Task.Total != 10 || d.Task.Finished != 3 || d.Metric.RetrievalMetrics.Precision != 0.75 {
			t.Fatal("detail lost")
		}
		if id == "c" && d.Task.Status != types.EvaluationStatueSuccess {
			t.Fatal("success overwritten")
		}
		if (id == "a" || id == "b") && (d.Task.Status != types.EvaluationStatueFailed || d.Task.ErrMsg == "") {
			t.Fatal("interrupted task not marked failed")
		}
		if _, err = reopened.get(8, id); err == nil {
			t.Fatal("cross tenant read")
		}
	}
}
func TestEvaluationStorageSnapshotsAndFailedWrites(t *testing.T) {
	db := evaluationTestDB(t)
	store, err := newEvaluationDBStorage(db)
	if err != nil {
		t.Fatal(err)
	}
	d := &types.EvaluationDetail{Task: &types.EvaluationTask{ID: "a", TenantID: 7}, Params: &types.ChatManage{}}
	if err = store.register(d); err != nil {
		t.Fatal(err)
	}
	d.Task.Total = 99
	saved, err := store.get(7, "a")
	if err != nil {
		t.Fatal(err)
	}
	if saved.Task.Total != 0 {
		t.Fatal("shared pointer on register")
	}
	saved.Task.Total = 88
	if err = store.update(7, "a", func(d *types.EvaluationDetail) { d.Task.Finished = 1 }); err != nil {
		t.Fatal(err)
	}
	saved, err = store.get(7, "a")
	if err != nil || saved.Task.Total != 0 || saved.Task.Finished != 1 {
		t.Fatalf("snapshot update: %+v %v", saved, err)
	}
	if err = store.update(8, "a", func(d *types.EvaluationDetail) { d.Task.Total = 55 }); err == nil {
		t.Fatal("cross tenant write")
	}
	db.Migrator().DropTable(&evaluationRecord{})
	if err = store.register(d); err == nil {
		t.Fatal("register swallowed SQL error")
	}
	if err = store.update(7, "a", func(*types.EvaluationDetail) {}); err == nil {
		t.Fatal("update swallowed SQL error")
	}
}
func TestEvaluationConcurrentUpdatesPreserveCounts(t *testing.T) {
	db := evaluationTestDB(t)
	store, err := newEvaluationDBStorage(db)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.register(&types.EvaluationDetail{Task: &types.EvaluationTask{ID: "count", TenantID: 7}}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- store.update(7, "count", func(d *types.EvaluationDetail) { d.Task.Finished++ })
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	detail, err := store.get(7, "count")
	if err != nil || detail.Task.Finished != 20 {
		t.Fatalf("lost progress: %+v %v", detail, err)
	}
}
func TestMemoryEvaluationReturnsSnapshots(t *testing.T) {
	store := newEvaluationMemoryStorage()
	d := &types.EvaluationDetail{Task: &types.EvaluationTask{ID: "a", TenantID: 7}}
	if err := store.register(d); err != nil {
		t.Fatal(err)
	}
	d.Task.Status = types.EvaluationStatueFailed
	snapshot, err := store.get(7, "a")
	if err != nil || snapshot.Task.Status != types.EvaluationStatuePending {
		t.Fatalf("shared input: %+v %v", snapshot, err)
	}
	snapshot.Task.Total = 50
	next, _ := store.get(7, "a")
	if next.Task.Total != 0 {
		t.Fatal("shared output")
	}
	if _, err = store.get(8, "a"); err == nil {
		t.Fatal("cross tenant memory read")
	}
}
