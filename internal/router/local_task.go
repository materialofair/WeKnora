package router

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"gorm.io/gorm"
)

// localTask is durable in the application SQLite database. Delivery is at least
// once: interrupted handlers are replayed, so external effects must be idempotent.
type localTask struct {
	ID          string `gorm:"primaryKey"`
	Type        string
	Payload     []byte
	Queue       string
	State       string    `gorm:"index:local_tasks_ready"`
	AvailableAt time.Time `gorm:"index:local_tasks_ready"`
	Attempt     int
	MaxRetry    int
	Timeout     time.Duration
	Deadline    time.Time
	Retention   time.Duration
	FinishedAt  *time.Time
	LastError   string
	CreatedAt   time.Time
}

type localTaskExecutor struct {
	noopTaskInspector
	db          *gorm.DB
	mu          sync.Mutex
	active      map[string]context.CancelFunc
	ctx         context.Context
	cancel      context.CancelFunc
	wg          sync.WaitGroup
	started     bool
	retryDelay  time.Duration
	lastCleanup time.Time
}

func NewDurableTaskExecutor(db *gorm.DB) (*SyncTaskExecutor, error) {
	if err := db.AutoMigrate(&localTask{}); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	e := NewSyncTaskExecutor()
	e.local = &localTaskExecutor{db: db, active: make(map[string]context.CancelFunc), ctx: ctx, cancel: cancel, retryDelay: 5 * time.Second}
	return e, nil
}

func (e *localTaskExecutor) enqueue(task *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo, error) {
	row := localTask{ID: uuid.NewString(), Type: task.Type(), Payload: task.Payload(), Queue: "default", State: "pending", AvailableAt: time.Now(), MaxRetry: 25, Timeout: 30 * time.Minute, Retention: 0}
	for _, opt := range opts {
		switch opt.Type() {
		case asynq.TaskIDOpt:
			row.ID = opt.Value().(string)
		case asynq.QueueOpt:
			row.Queue = opt.Value().(string)
		case asynq.ProcessInOpt:
			row.AvailableAt = time.Now().Add(opt.Value().(time.Duration))
		case asynq.ProcessAtOpt:
			row.AvailableAt = opt.Value().(time.Time)
		case asynq.MaxRetryOpt:
			row.MaxRetry = max(0, opt.Value().(int))
		case asynq.TimeoutOpt:
			row.Timeout = opt.Value().(time.Duration)
		case asynq.DeadlineOpt:
			row.Deadline = opt.Value().(time.Time)
		case asynq.RetentionOpt:
			row.Retention = opt.Value().(time.Duration)
		default:
			return nil, fmt.Errorf("unsupported local task option %v", opt.Type())
		}
	}
	if row.ID == "" || row.Queue == "" || row.Timeout < 0 || row.Retention < 0 {
		return nil, fmt.Errorf("invalid local task options")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.ctx.Err() != nil {
		return nil, fmt.Errorf("local executor is shutting down")
	}
	var existing localTask
	found := e.db.Where("id = ?", row.ID).Limit(1).Find(&existing)
	if found.Error != nil {
		return nil, found.Error
	}
	if found.RowsAffected > 0 && (existing.State == "completed" || existing.State == "cancelled") && existing.FinishedAt != nil && time.Since(*existing.FinishedAt) >= existing.Retention {
		if err := e.db.Where("id = ?", row.ID).Delete(&localTask{}).Error; err != nil {
			return nil, err
		}
	}
	var count int64
	if err := e.db.Model(&localTask{}).Where("id = ?", row.ID).Count(&count).Error; err != nil {
		return nil, err
	}
	if count > 0 {
		return nil, asynq.ErrTaskIDConflict
	}
	if err := e.db.Create(&row).Error; err != nil {
		return nil, err
	}
	return &asynq.TaskInfo{ID: row.ID, Type: row.Type, Queue: row.Queue, Payload: row.Payload, MaxRetry: row.MaxRetry, NextProcessAt: row.AvailableAt}, nil
}

// Start is invoked only after every handler has been registered.
func (e *localTaskExecutor) Start(owner *SyncTaskExecutor) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.started {
		return nil
	}
	if err := e.db.Model(&localTask{}).Where("state = ?", "active").Updates(map[string]any{"state": "pending", "available_at": time.Now()}).Error; err != nil {
		return err
	}
	e.started = true
	for range 4 {
		e.wg.Add(1)
		go e.worker(owner)
	}
	return nil
}
func (e *localTaskExecutor) Close() error {
	e.cancel()
	done := make(chan struct{})
	go func() { e.wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-time.After(10 * time.Second):
		return fmt.Errorf("local tasks did not stop within 10s; interrupted tasks will recover on next launch")
	}
}
func (e *localTaskExecutor) worker(owner *SyncTaskExecutor) {
	defer e.wg.Done()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-e.ctx.Done():
			return
		case <-ticker.C:
		}
		row, ctx, cancel, err := e.claim()
		if err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				logger.Errorf(e.ctx, "Local task claim failed: %v", err)
			}
			continue
		}
		owner.mu.RLock()
		handler := owner.handlers[row.Type]
		owner.mu.RUnlock()
		taskCtx := types.WithTaskRetryMetadata(types.WithBackgroundTask(types.WithBackgroundTaskID(ctx, row.ID)), row.Attempt, row.MaxRetry)
		err = invokeLocalHandler(taskCtx, handler, asynq.NewTask(row.Type, row.Payload))
		cancel()
		e.finish(row, err)
	}
}
func invokeLocalHandler(ctx context.Context, handler func(context.Context, *asynq.Task) error, task *asynq.Task) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("task handler panic: %v", r)
		}
	}()
	if handler == nil {
		return fmt.Errorf("no handler for %q: %w", task.Type(), asynq.SkipRetry)
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	return handler(ctx, task)
}
func (e *localTaskExecutor) claim() (localTask, context.Context, context.CancelFunc, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	var row localTask
	if e.ctx.Err() != nil {
		return row, nil, nil, e.ctx.Err()
	}
	// Expire terminal records; retention is per task and stored in nanoseconds.
	// Use Go timestamps for dialect-independent cleanup rather than SQL date math.
	if time.Since(e.lastCleanup) > time.Minute {
		e.lastCleanup = time.Now()
		var expired []localTask
		if err := e.db.Where("state IN ?", []string{"completed", "cancelled"}).Order("finished_at ASC").Limit(100).Find(&expired).Error; err == nil {
			for _, r := range expired {
				if r.FinishedAt != nil && time.Since(*r.FinishedAt) >= r.Retention {
					e.db.Delete(&localTask{}, "id = ?", r.ID)
				}
			}
		}
	}
	result := e.db.Where("state IN ? AND available_at <= ?", []string{"pending", "retry"}, time.Now()).Order("available_at ASC").Limit(1).Find(&row)
	if result.Error != nil {
		return row, nil, nil, result.Error
	}
	if result.RowsAffected == 0 {
		return row, nil, nil, gorm.ErrRecordNotFound
	}
	if err := e.db.Model(&localTask{}).Where("id = ?", row.ID).Update("state", "active").Error; err != nil {
		return row, nil, nil, err
	}
	deadline := time.Now().Add(row.Timeout)
	if row.Timeout == 0 {
		deadline = time.Now().Add(30 * time.Minute)
	}
	if !row.Deadline.IsZero() && row.Deadline.Before(deadline) {
		deadline = row.Deadline
	}
	ctx, cancel := context.WithDeadline(e.ctx, deadline)
	e.active[row.ID] = cancel
	return row, ctx, cancel, nil
}
func (e *localTaskExecutor) finish(row localTask, runErr error) {
	// Retrying an outcome write must never rerun the handler. Keep the worker
	// occupied until its result is durable, allowing a transient disk/DB error
	// to recover without leaving an unreachable active record until restart.
	for {
		err := e.persistOutcome(row, runErr)
		if err == nil {
			return
		}
		logger.Errorf(e.ctx, "Local task %s outcome persistence failed; retrying without re-executing handler: %v", row.ID, err)
		select {
		case <-e.ctx.Done():
			e.mu.Lock()
			delete(e.active, row.ID)
			e.mu.Unlock()
			return // Durable active record is recovered by the next process.
		case <-time.After(time.Second):
		}
	}
}
func (e *localTaskExecutor) persistOutcome(row localTask, runErr error) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if runErr == nil && row.Retention == 0 {
		err := e.db.Where("id = ? AND state = ?", row.ID, "active").Delete(&localTask{}).Error
		if err == nil {
			delete(e.active, row.ID)
		}
		return err
	}
	now := time.Now()
	updates := map[string]any{"state": "completed", "finished_at": now, "last_error": ""}
	if runErr != nil {
		updates["last_error"] = runErr.Error()
		if e.ctx.Err() != nil {
			updates["state"] = "pending"
			updates["available_at"] = now
			updates["finished_at"] = nil
		} else if row.Attempt < row.MaxRetry && !errors.Is(runErr, asynq.SkipRetry) {
			updates["state"] = "retry"
			updates["attempt"] = row.Attempt + 1
			updates["available_at"] = now.Add(time.Duration(min(row.Attempt+1, 6)) * e.retryDelay)
			updates["finished_at"] = nil
		} else {
			updates["state"] = "archived"
		}
	}
	err := e.db.Model(&localTask{}).Where("id = ? AND state = ?", row.ID, "active").Updates(updates).Error
	if err == nil {
		delete(e.active, row.ID)
	}
	return err
}

func (e *localTaskExecutor) pending(ctx context.Context) ([]localTask, error) {
	var rows []localTask
	err := e.db.WithContext(ctx).Where("state IN ?", []string{"pending", "retry", "active"}).Find(&rows).Error
	return rows, err
}
func (e *localTaskExecutor) HasQueuedTasksForKnowledge(ctx context.Context, id string) (bool, error) {
	rows, err := e.pending(ctx)
	for _, r := range rows {
		if matchesKnowledge(r.Type, r.Payload, id) {
			return true, err
		}
	}
	return false, err
}
func (e *localTaskExecutor) HasQueuedDeleteTasksForKnowledge(ctx context.Context, id string) (bool, error) {
	rows, err := e.pending(ctx)
	for _, r := range rows {
		if matchesKnowledgeListDelete(r.Type, r.Payload, id) {
			return true, err
		}
	}
	return false, err
}
func (e *localTaskExecutor) CancelTasksForKnowledge(ctx context.Context, id string) (int, int, error) {
	return e.cancelMatching(ctx, func(r localTask) bool { return matchesKnowledge(r.Type, r.Payload, id) })
}
func (e *localTaskExecutor) CancelTasksForKnowledgeBase(ctx context.Context, id string, knowledgeIDs, dataSourceIDs []string) (int, int, error) {
	ks, ds := map[string]struct{}{}, map[string]struct{}{}
	for _, k := range knowledgeIDs {
		ks[k] = struct{}{}
	}
	for _, d := range dataSourceIDs {
		ds[d] = struct{}{}
	}
	return e.cancelMatching(ctx, func(r localTask) bool { return matchesKnowledgeBase(r.Type, r.Payload, id, ks, ds) })
}
func (e *localTaskExecutor) cancelMatching(ctx context.Context, match func(localTask) bool) (int, int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	rows, err := e.pending(ctx)
	if err != nil {
		return 0, 0, err
	}
	deleted, cancelled := 0, 0
	for _, r := range rows {
		if !match(r) {
			continue
		}
		if err = e.db.WithContext(ctx).Model(&localTask{}).Where("id = ?", r.ID).Updates(map[string]any{"state": "cancelled", "finished_at": time.Now()}).Error; err != nil {
			return deleted, cancelled, err
		}
		if cancel := e.active[r.ID]; cancel != nil {
			cancel()
			cancelled++
		} else {
			deleted++
		}
	}
	return deleted, cancelled, nil
}

func localProjection(row localTask) (types.RuntimeTaskInfo, error) {
	state := asynq.TaskStatePending
	switch row.State {
	case "active":
		state = asynq.TaskStateActive
	case "retry":
		state = asynq.TaskStateRetry
	case "archived":
		state = asynq.TaskStateArchived
	case "completed", "cancelled":
		state = asynq.TaskStateCompleted
	case "pending":
		if row.AvailableAt.After(time.Now()) {
			state = asynq.TaskStateScheduled
		}
	}
	task := &asynq.TaskInfo{ID: row.ID, Type: row.Type, Queue: row.Queue, Payload: row.Payload, State: state, LastErr: row.LastError, NextProcessAt: row.AvailableAt, Retried: row.Attempt, MaxRetry: row.MaxRetry, Deadline: row.Deadline}
	if row.FinishedAt != nil {
		task.CompletedAt = *row.FinishedAt
	}
	result, err := projectRuntimeTask(task, runtimeWorkerMetadata{})
	result.EnqueuedAt = &row.CreatedAt
	return result, err
}
func (e *localTaskExecutor) QueueStats(ctx context.Context) ([]types.QueueStat, bool, error) {
	var groups []struct {
		Queue, State string
		Count        int
	}
	err := e.db.WithContext(ctx).Model(&localTask{}).Select("queue,state,count(*) as count").Group("queue,state").Scan(&groups).Error
	if err != nil {
		return nil, true, err
	}
	byQueue := map[string]*types.QueueStat{}
	for _, g := range groups {
		s := byQueue[g.Queue]
		if s == nil {
			s = &types.QueueStat{Name: g.Queue, Pool: "local", Weight: 1}
			byQueue[g.Queue] = s
		}
		s.Size += g.Count
		switch g.State {
		case "pending":
			s.Pending += g.Count
		case "active":
			s.Active += g.Count
		case "retry":
			s.Retry += g.Count
		case "archived":
			s.Archived += g.Count
		case "completed":
			s.Completed += g.Count
		}
	}
	result := make([]types.QueueStat, 0, len(byQueue))
	for _, s := range byQueue {
		result = append(result, *s)
	}
	return result, true, nil
}
func (e *localTaskExecutor) WorkerServerStats(ctx context.Context) ([]types.WorkerServerStat, bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return []types.WorkerServerStat{{Concurrency: 4, Active: len(e.active), Status: "active", Queues: map[string]int{"local": 1}}}, true, nil
}
func (e *localTaskExecutor) ListRuntimeTasks(ctx context.Context, queue string, state types.RuntimeTaskState, cursor string, pageSize int) (types.RuntimeTaskPage, bool, error) {
	page := types.RuntimeTaskPage{Tasks: []types.RuntimeTaskInfo{}}
	if !state.Valid() {
		return page, true, fmt.Errorf("invalid task state")
	}
	pageSize = max(1, min(pageSize, 100))
	query := e.db.WithContext(ctx).Where("queue = ?", queue)
	switch state {
	case types.RuntimeTaskScheduled:
		query = query.Where("state = ? AND available_at > ?", "pending", time.Now())
	case types.RuntimeTaskPending:
		query = query.Where("state = ? AND available_at <= ?", "pending", time.Now())
	default:
		query = query.Where("state = ?", string(state))
	}
	if cursor != "" {
		prefix := queue + ":" + string(state) + ":"
		decoded, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil || !strings.HasPrefix(string(decoded), prefix) {
			return page, true, types.ErrInvalidRuntimeTaskCursor
		}
		query = query.Where("id > ?", strings.TrimPrefix(string(decoded), prefix))
	}
	var rows []localTask
	if err := query.Order("id ASC").Limit(pageSize + 1).Find(&rows).Error; err != nil {
		return page, true, err
	}
	page.HasMore = len(rows) > pageSize
	if page.HasMore {
		rows = rows[:pageSize]
	}
	for _, row := range rows {
		info, err := localProjection(row)
		if err != nil {
			return page, true, err
		}
		page.Tasks = append(page.Tasks, info)
	}
	if page.HasMore {
		page.NextCursor = base64.RawURLEncoding.EncodeToString([]byte(queue + ":" + string(state) + ":" + rows[len(rows)-1].ID))
	}
	return page, true, nil
}
func (e *localTaskExecutor) GetRuntimeTask(ctx context.Context, queue, id string) (*types.RuntimeTaskInfo, bool, error) {
	var row localTask
	if err := e.db.WithContext(ctx).Where("queue = ? AND id = ?", queue, id).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			err = asynq.ErrTaskNotFound
		}
		return nil, true, err
	}
	info, err := localProjection(row)
	return &info, true, err
}
func (e *localTaskExecutor) RunRuntimeTask(ctx context.Context, queue, id string) (bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	info, _, err := e.GetRuntimeTask(ctx, queue, id)
	if err != nil {
		return true, err
	}
	if !info.Allows(types.RuntimeTaskActionRunNow) {
		return true, fmt.Errorf("task cannot be run in state %s", info.State)
	}
	return true, e.db.WithContext(ctx).Model(&localTask{}).Where("queue = ? AND id = ?", queue, id).Updates(map[string]any{"state": "pending", "available_at": time.Now(), "attempt": 0, "finished_at": nil}).Error
}
func (e *localTaskExecutor) DeleteRuntimeTask(ctx context.Context, queue, id string) (bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	info, _, err := e.GetRuntimeTask(ctx, queue, id)
	if err != nil {
		return true, err
	}
	if !info.Allows(types.RuntimeTaskActionDelete) {
		return true, fmt.Errorf("task cannot be deleted in state %s", info.State)
	}
	return true, e.db.WithContext(ctx).Delete(&localTask{}, "queue = ? AND id = ?", queue, id).Error
}
func (e *localTaskExecutor) ForceDeleteRuntimeTask(ctx context.Context, queue, id string) (bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if cancel := e.active[id]; cancel != nil {
		cancel()
	}
	return true, e.db.WithContext(ctx).Delete(&localTask{}, "queue = ? AND id = ?", queue, id).Error
}
func (e *localTaskExecutor) PurgeArchivedRuntimeTasks(ctx context.Context, queue string) (int, bool, error) {
	result := e.db.WithContext(ctx).Delete(&localTask{}, "queue = ? AND state = ?", queue, "archived")
	return int(result.RowsAffected), true, result.Error
}
