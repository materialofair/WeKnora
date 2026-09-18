package config

import (
	"os"
	"strings"
)

// GraphEnabled reports whether an actual graph storage backend is configured.
func GraphEnabled() bool {
	return strings.EqualFold(os.Getenv("GRAPH_DRIVER"), "sqlite") || strings.EqualFold(os.Getenv("NEO4J_ENABLE"), "true")
}
