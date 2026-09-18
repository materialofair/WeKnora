package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/gorm"
)

type evaluationStorage interface {
	register(*types.EvaluationDetail) error
	get(uint64, string) (*types.EvaluationDetail, error)
	update(uint64, string, func(*types.EvaluationDetail)) error
}

type evaluationRecord struct {
	ID        string                 `gorm:"primaryKey"`
	TenantID  uint64                 `gorm:"index"`
	Status    types.EvaluationStatue `gorm:"index"`
	Detail    []byte
	UpdatedAt time.Time
}

type evaluationDBStorage struct {
	db *gorm.DB
	mu sync.Mutex
}

func newEvaluationDBStorage(db *gorm.DB) (*evaluationDBStorage, error) {
	if err := db.AutoMigrate(&evaluationRecord{}); err != nil {
		return nil, err
	}
	store := &evaluationDBStorage{db: db}
	// Evaluations create temporary knowledge and invoke models. Replaying them
	// automatically can duplicate side effects; expose interrupted results instead.
	err := db.Transaction(func(tx *gorm.DB) error {
		var records []evaluationRecord
		if err := tx.Where("status IN ?", []types.EvaluationStatue{types.EvaluationStatuePending, types.EvaluationStatueRunning}).Find(&records).Error; err != nil {
			return err
		}
		for _, row := range records {
			detail, err := decodeEvaluation(row.Detail)
			if err != nil {
				return err
			}
			detail.Task.Status = types.EvaluationStatueFailed
			detail.Task.ErrMsg = "Evaluation interrupted by application restart; run a new evaluation to retry"
			payload, err := json.Marshal(detail)
			if err != nil {
				return err
			}
			if err = tx.Model(&evaluationRecord{}).Where("id = ? AND tenant_id = ?", row.ID, row.TenantID).Updates(map[string]any{"status": types.EvaluationStatueFailed, "detail": payload, "updated_at": time.Now()}).Error; err != nil {
				return err
			}
		}
		return nil
	})
	return store, err
}
func decodeEvaluation(data []byte) (*types.EvaluationDetail, error) {
	var detail types.EvaluationDetail
	if err := json.Unmarshal(data, &detail); err != nil {
		return nil, fmt.Errorf("invalid stored evaluation: %w", err)
	}
	if detail.Task == nil {
		return nil, errors.New("stored evaluation task is missing")
	}
	return &detail, nil
}
func (s *evaluationDBStorage) register(detail *types.EvaluationDetail) error {
	if detail == nil || detail.Task == nil {
		return errors.New("evaluation task is required")
	}
	payload, err := json.Marshal(detail)
	if err != nil {
		return err
	}
	return s.db.Create(&evaluationRecord{ID: detail.Task.ID, TenantID: detail.Task.TenantID, Status: detail.Task.Status, Detail: payload}).Error
}
func (s *evaluationDBStorage) get(tenantID uint64, id string) (*types.EvaluationDetail, error) {
	var row evaluationRecord
	if err := s.db.Where("id = ? AND tenant_id = ?", id, tenantID).First(&row).Error; err != nil {
		return nil, err
	}
	return decodeEvaluation(row.Detail)
}
func (s *evaluationDBStorage) update(tenantID uint64, id string, fn func(*types.EvaluationDetail)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.db.Transaction(func(tx *gorm.DB) error {
		var row evaluationRecord
		if err := tx.Where("id = ? AND tenant_id = ?", id, tenantID).First(&row).Error; err != nil {
			return err
		}
		detail, err := decodeEvaluation(row.Detail)
		if err != nil {
			return err
		}
		fn(detail)
		if detail.Task.ID != id || detail.Task.TenantID != tenantID {
			return errors.New("evaluation identity cannot change")
		}
		payload, err := json.Marshal(detail)
		if err != nil {
			return err
		}
		return tx.Model(&evaluationRecord{}).Where("id = ? AND tenant_id = ?", id, tenantID).Updates(map[string]any{"status": detail.Task.Status, "detail": payload, "updated_at": time.Now()}).Error
	})
}

// Memory mode also stores snapshots, so HTTP readers never share mutable task
// pointers with evaluation workers.
type evaluationMemoryStorage struct {
	mu    sync.RWMutex
	store map[string][]byte
}

func newEvaluationMemoryStorage() *evaluationMemoryStorage {
	return &evaluationMemoryStorage{store: map[string][]byte{}}
}
func (s *evaluationMemoryStorage) register(detail *types.EvaluationDetail) error {
	payload, err := json.Marshal(detail)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.store[detail.Task.ID]; ok {
		return errors.New("evaluation already exists")
	}
	s.store[detail.Task.ID] = payload
	return nil
}
func (s *evaluationMemoryStorage) get(tenantID uint64, id string) (*types.EvaluationDetail, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.getLocked(tenantID, id)
}
func (s *evaluationMemoryStorage) getLocked(tenantID uint64, id string) (*types.EvaluationDetail, error) {
	data, ok := s.store[id]
	if !ok {
		return nil, errors.New("task not found")
	}
	detail, err := decodeEvaluation(data)
	if err != nil {
		return nil, err
	}
	if detail.Task.TenantID != tenantID {
		return nil, errors.New("task not found")
	}
	return detail, nil
}
func (s *evaluationMemoryStorage) update(tenantID uint64, id string, fn func(*types.EvaluationDetail)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	detail, err := s.getLocked(tenantID, id)
	if err != nil {
		return err
	}
	fn(detail)
	if detail.Task.ID != id || detail.Task.TenantID != tenantID {
		return errors.New("evaluation identity cannot change")
	}
	payload, err := json.Marshal(detail)
	if err != nil {
		return err
	}
	s.store[id] = payload
	return nil
}
