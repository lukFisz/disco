package main

import (
	"testing"

	"disco/config"
)

// TestExampleConfigLoads guards against drift between config.yaml.example
// and the real config schema: if the example ever stops parsing/validating,
// this test catches it.
func TestExampleConfigLoads(t *testing.T) {
	cfg, err := config.Load("config.yaml.example")
	if err != nil {
		t.Fatalf("config.Load(\"config.yaml.example\") failed: %v", err)
	}
	if len(cfg.Commands) == 0 {
		t.Fatal("expected config.yaml.example to define at least one command")
	}
}
