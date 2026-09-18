package sqlitegraph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type node struct {
	KnowledgeBase string `gorm:"primaryKey"`
	Knowledge     string `gorm:"primaryKey"`
	Name          string `gorm:"primaryKey"`
	Chunks        string
	Attributes    string
}

func (node) TableName() string { return "portable_graph_nodes" }

type edge struct {
	KnowledgeBase string `gorm:"primaryKey"`
	Knowledge     string `gorm:"primaryKey"`
	Source        string `gorm:"primaryKey"`
	Target        string `gorm:"primaryKey"`
	Kind          string `gorm:"primaryKey"`
}

func (edge) TableName() string { return "portable_graph_edges" }

type Repository struct{ db *gorm.DB }

func New(db *gorm.DB) (interfaces.RetrieveGraphRepository, error) {
	if err := db.AutoMigrate(&node{}, &edge{}); err != nil {
		return nil, fmt.Errorf("graph schema: %w", err)
	}
	return &Repository{db: db}, nil
}

func scoped(db *gorm.DB, ns types.NameSpace) *gorm.DB {
	db = db.Where("knowledge_base = ?", ns.KnowledgeBase)
	if ns.Knowledge != "" {
		db = db.Where("knowledge = ?", ns.Knowledge)
	}
	return db
}

func mergeJSON(previous string, added []string) (string, error) {
	var values []string
	if previous != "" {
		if err := json.Unmarshal([]byte(previous), &values); err != nil {
			return "", err
		}
	}
	seen := make(map[string]bool)
	merged := make([]string, 0, len(values)+len(added))
	for _, value := range append(values, added...) {
		if !seen[value] {
			merged = append(merged, value)
			seen[value] = true
		}
	}
	data, err := json.Marshal(merged)
	return string(data), err
}

func (r *Repository) AddGraph(ctx context.Context, ns types.NameSpace, graphs []*types.GraphData) error {
	if ns.KnowledgeBase == "" || ns.Knowledge == "" {
		return errors.New("graph write requires knowledge base and document")
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, graph := range graphs {
			if graph == nil {
				continue
			}
			nodes := append([]*types.GraphNode(nil), graph.Node...)
			for _, rel := range graph.Relation {
				if rel == nil {
					continue
				}
				nodes = append(nodes, &types.GraphNode{Name: rel.Node1}, &types.GraphNode{Name: rel.Node2})
			}
			for _, n := range nodes {
				if n == nil || strings.TrimSpace(n.Name) == "" {
					continue
				}
				var old node
				err := scoped(tx, ns).Where("name = ?", n.Name).First(&old).Error
				if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
					return err
				}
				chunks, err := mergeJSON(old.Chunks, n.Chunks)
				if err != nil {
					return err
				}
				attrs, err := mergeJSON(old.Attributes, n.Attributes)
				if err != nil {
					return err
				}
				record := node{ns.KnowledgeBase, ns.Knowledge, n.Name, chunks, attrs}
				if err := tx.Clauses(clause.OnConflict{UpdateAll: true}).Create(&record).Error; err != nil {
					return err
				}
			}
			for _, rel := range graph.Relation {
				if rel == nil || rel.Node1 == "" || rel.Node2 == "" {
					continue
				}
				record := edge{ns.KnowledgeBase, ns.Knowledge, rel.Node1, rel.Node2, rel.Type}
				if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&record).Error; err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func (r *Repository) DelGraph(ctx context.Context, namespaces []types.NameSpace) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, ns := range namespaces {
			if ns.KnowledgeBase == "" {
				return errors.New("graph deletion requires knowledge base")
			}
			if err := scoped(tx, ns).Delete(&edge{}).Error; err != nil {
				return err
			}
			if err := scoped(tx, ns).Delete(&node{}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *Repository) SearchNode(ctx context.Context, ns types.NameSpace, terms []string) (*types.GraphData, error) {
	if ns.KnowledgeBase == "" {
		return nil, errors.New("graph search requires knowledge base")
	}
	result := &types.GraphData{Node: []*types.GraphNode{}, Relation: []*types.GraphRelation{}}
	conditions, args := []string{}, []any{}
	for _, term := range terms {
		if strings.TrimSpace(term) != "" {
			conditions = append(conditions, "instr(lower(name), lower(?)) > 0")
			args = append(args, term)
		}
	}
	if len(conditions) == 0 {
		return result, nil
	}
	db := r.db.WithContext(ctx)
	var matches []node
	if err := scoped(db, ns).Where(strings.Join(conditions, " OR "), args...).Order("knowledge, name").Limit(1000).Find(&matches).Error; err != nil {
		return nil, err
	}
	seenNodes := map[string]*types.GraphNode{}
	seenEdges := map[string]bool{}
	addNode := func(n node) error {
		var chunks, attrs []string
		if err := json.Unmarshal([]byte(n.Chunks), &chunks); err != nil {
			return err
		}
		if err := json.Unmarshal([]byte(n.Attributes), &attrs); err != nil {
			return err
		}
		if old := seenNodes[n.Name]; old != nil {
			merged, _ := mergeJSON(mustJSON(old.Chunks), chunks)
			_ = json.Unmarshal([]byte(merged), &old.Chunks)
			merged, _ = mergeJSON(mustJSON(old.Attributes), attrs)
			_ = json.Unmarshal([]byte(merged), &old.Attributes)
		} else {
			nn := &types.GraphNode{Name: n.Name, Chunks: chunks, Attributes: attrs}
			seenNodes[n.Name] = nn
			result.Node = append(result.Node, nn)
		}
		return nil
	}
	for _, match := range matches {
		if err := addNode(match); err != nil {
			return nil, err
		}
	}
	// Bound the whole expansion, not each matching node. Fetch neighbours in
	// batches to avoid one database query per edge on a shared SQLite connection.
	selected := scoped(db.Model(&node{}), ns).Select("knowledge, name").Where(strings.Join(conditions, " OR "), args...).Order("knowledge, name").Limit(1000)
	var edges []edge
	if err := scoped(db, ns).Where("(knowledge, source) IN (?) OR (knowledge, target) IN (?)", selected, selected).Order("knowledge, source, target, kind").Limit(2000).Find(&edges).Error; err != nil {
		return nil, err
	}
	keys := make([][]any, 0, len(edges)*2)
	seenKeys := map[string]bool{}
	for _, e := range edges {
		key := mustJSON([]string{e.Source, e.Target, e.Kind})
		if !seenEdges[key] {
			result.Relation = append(result.Relation, &types.GraphRelation{Node1: e.Source, Node2: e.Target, Type: e.Kind})
			seenEdges[key] = true
		}
		for _, name := range []string{e.Source, e.Target} {
			key := mustJSON([]string{e.Knowledge, name})
			if !seenKeys[key] {
				seenKeys[key] = true
				keys = append(keys, []any{e.Knowledge, name})
			}
		}
	}
	for start := 0; start < len(keys); start += 300 {
		end := start + 300
		if end > len(keys) {
			end = len(keys)
		}
		var adjacent []node
		if err := scoped(db, ns).Where("(knowledge, name) IN ?", keys[start:end]).Order("knowledge, name").Find(&adjacent).Error; err != nil {
			return nil, err
		}
		for _, n := range adjacent {
			if err := addNode(n); err != nil {
				return nil, err
			}
		}
	}
	return result, nil
}

func mustJSON(values []string) string { data, _ := json.Marshal(values); return string(data) }
