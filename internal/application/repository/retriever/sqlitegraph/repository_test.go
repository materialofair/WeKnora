package sqlitegraph

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestGraphPersistenceNamespacesAndDeletion(t *testing.T) {
	file := filepath.Join(t.TempDir(), "graph.db")
	db, err := gorm.Open(sqlite.Open(file), &gorm.Config{})
	require.NoError(t, err)
	r, err := New(db)
	require.NoError(t, err)
	ctx := context.Background()
	ns := types.NameSpace{KnowledgeBase: "team-a", Knowledge: "doc-1"}
	graph := &types.GraphData{Node: []*types.GraphNode{{Name: "研发部门", Chunks: []string{"c1"}}, {Name: "项目", Chunks: []string{"c2"}}}, Relation: []*types.GraphRelation{{Node1: "研发部门", Node2: "项目", Type: "负责"}}}
	require.NoError(t, r.AddGraph(ctx, ns, []*types.GraphData{graph}))
	require.NoError(t, r.AddGraph(ctx, ns, []*types.GraphData{{Node: []*types.GraphNode{{Name: "研发部门", Chunks: []string{"c3"}}}}}))
	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())
	db, err = gorm.Open(sqlite.Open(file), &gorm.Config{})
	require.NoError(t, err)
	t.Cleanup(func() { handle, _ := db.DB(); _ = handle.Close() })
	r, err = New(db)
	require.NoError(t, err)
	got, err := r.SearchNode(ctx, types.NameSpace{KnowledgeBase: "team-a"}, []string{"研发"})
	require.NoError(t, err)
	require.Len(t, got.Node, 2)
	require.Len(t, got.Relation, 1)
	for _, n := range got.Node {
		if n.Name == "研发部门" {
			require.ElementsMatch(t, []string{"c1", "c3"}, n.Chunks)
		}
	}
	other, err := r.SearchNode(ctx, types.NameSpace{KnowledgeBase: "team-b"}, []string{"研发"})
	require.NoError(t, err)
	require.Empty(t, other.Node)
	_, err = r.SearchNode(ctx, types.NameSpace{}, []string{"研发"})
	require.Error(t, err)
	require.Error(t, r.DelGraph(ctx, []types.NameSpace{{}}))
	require.NoError(t, r.DelGraph(ctx, []types.NameSpace{ns}))
	got, err = r.SearchNode(ctx, ns, []string{"研发"})
	require.NoError(t, err)
	require.Empty(t, got.Node)
}

func TestGraphNeighbourQueriesAreBatched(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "graph.db")), &gorm.Config{})
	require.NoError(t, err)
	r, err := New(db)
	require.NoError(t, err)
	ns := types.NameSpace{KnowledgeBase: "kb", Knowledge: "doc"}
	graph := &types.GraphData{}
	for i := 0; i < 30; i++ {
		name := fmt.Sprintf("node-%d", i)
		graph.Node = append(graph.Node, &types.GraphNode{Name: name})
		for j := 0; j < i; j++ {
			graph.Relation = append(graph.Relation, &types.GraphRelation{Node1: name, Node2: fmt.Sprintf("node-%d", j), Type: "related"})
		}
	}
	require.NoError(t, r.AddGraph(context.Background(), ns, []*types.GraphData{graph}))
	queries := 0
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register("count_queries", func(*gorm.DB) { queries++ }))
	result, err := r.SearchNode(context.Background(), ns, []string{"node"})
	require.NoError(t, err)
	require.Len(t, result.Node, 30)
	require.Len(t, result.Relation, 435)
	// GORM also invokes the callback while compiling the two IN subqueries.
	require.LessOrEqual(t, queries, 5)
}
