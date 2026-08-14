package main

import (
	"testing"

	"github.com/smallnest/pigo/internal/session"
)

func TestResolvePromptRunStateUsesLaneConfig(t *testing.T) {
	proj := &session.ProjectLeaf{Model: "lane/model", Provider: "lane/provider", ThinkingLevel: "high"}
	got := resolvePromptRunState(proj, "env/model", "env/provider", "medium")
	if got.model != "lane/model" || got.provider != "lane/provider" || got.thinking != "high" {
		t.Fatalf("state = %+v, want lane.config values", got)
	}

	got = resolvePromptRunState(nil, "env/model", "env/provider", "medium")
	if got.model != "env/model" || got.provider != "env/provider" || got.thinking != "medium" {
		t.Fatalf("fallback state = %+v, want process defaults", got)
	}
}
