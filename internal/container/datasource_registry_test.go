package container

import (
	"github.com/Tencent/WeKnora/internal/types"
	"testing"
)

func TestConnectorRegistryRetainsOnlyFeishuAndLark(t *testing.T) {
	registry, err := initConnectorRegistry()
	if err != nil {
		t.Fatal(err)
	}
	if len(registry.List()) != 4 {
		t.Fatalf("expected 4 connectors, got %v", registry.List())
	}
	for _, kind := range []string{types.ConnectorTypeFeishu, types.ConnectorTypeLark, types.ConnectorTypeFeishuDrive, types.ConnectorTypeLarkDrive} {
		connector, err := registry.Get(kind)
		if err != nil {
			t.Fatalf("missing %s: %v", kind, err)
		}
		if connector.Type() != kind {
			t.Fatalf("wrong connector: %s", connector.Type())
		}
	}
	for _, kind := range []string{"gitlab", "notion", "confluence", "yuque", "dingtalk", "rss", "ima"} {
		if _, err := registry.Get(kind); err == nil {
			t.Fatalf("excluded connector %s is still registered", kind)
		}
	}
}
