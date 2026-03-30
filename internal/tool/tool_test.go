package tool

import (
	"context"
	"encoding/json"
	"testing"
)

// stubTool is a minimal Tool implementation for testing.
type stubTool struct {
	name string
}

func (s *stubTool) Name() string                                              { return s.name }
func (s *stubTool) Description() string                                       { return s.name + " tool" }
func (s *stubTool) Schema() json.RawMessage                                   { return json.RawMessage(`{}`) }
func (s *stubTool) Execute(ctx context.Context, params json.RawMessage) (*Result, error) { return &Result{Content: "ok"}, nil }

func TestRegistryFilter(t *testing.T) {
	r := NewRegistry()
	r.Register(&stubTool{name: "bash"})
	r.Register(&stubTool{name: "read"})
	r.Register(&stubTool{name: "write"})
	r.Register(&stubTool{name: "edit"})

	t.Run("empty allowlist returns original", func(t *testing.T) {
		filtered := r.Filter(nil)
		if filtered != r {
			t.Error("expected same registry for empty allowlist")
		}
	})

	t.Run("filter to subset", func(t *testing.T) {
		filtered := r.Filter([]string{"bash", "read"})
		if len(filtered.All()) != 2 {
			t.Errorf("expected 2 tools, got %d", len(filtered.All()))
		}
		if _, ok := filtered.Get("bash"); !ok {
			t.Error("expected bash in filtered registry")
		}
		if _, ok := filtered.Get("read"); !ok {
			t.Error("expected read in filtered registry")
		}
		if _, ok := filtered.Get("write"); ok {
			t.Error("write should not be in filtered registry")
		}
	})

	t.Run("filter with unknown names", func(t *testing.T) {
		filtered := r.Filter([]string{"bash", "nonexistent"})
		if len(filtered.All()) != 1 {
			t.Errorf("expected 1 tool, got %d", len(filtered.All()))
		}
	})
}

func TestRegistryNames(t *testing.T) {
	r := NewRegistry()
	r.Register(&stubTool{name: "write"})
	r.Register(&stubTool{name: "bash"})
	r.Register(&stubTool{name: "read"})

	names := r.Names()
	if len(names) != 3 {
		t.Fatalf("expected 3 names, got %d", len(names))
	}
	// Should be sorted
	if names[0] != "bash" || names[1] != "read" || names[2] != "write" {
		t.Errorf("expected sorted names [bash read write], got %v", names)
	}
}

func TestRegistryNames_Empty(t *testing.T) {
	r := NewRegistry()
	names := r.Names()
	if len(names) != 0 {
		t.Errorf("expected 0 names, got %d", len(names))
	}
}

func TestRegistryRegisterAndGet(t *testing.T) {
	r := NewRegistry()
	r.Register(&stubTool{name: "bash"})

	tool, ok := r.Get("bash")
	if !ok {
		t.Fatal("expected to find 'bash'")
	}
	if tool.Name() != "bash" {
		t.Errorf("Name() = %q, want 'bash'", tool.Name())
	}

	_, ok = r.Get("nonexistent")
	if ok {
		t.Error("should not find nonexistent tool")
	}
}

func TestRegistryAll(t *testing.T) {
	r := NewRegistry()
	r.Register(&stubTool{name: "a"})
	r.Register(&stubTool{name: "b"})
	r.Register(&stubTool{name: "c"})

	all := r.All()
	if len(all) != 3 {
		t.Errorf("expected 3 tools, got %d", len(all))
	}
}

func TestRegistryRegister_Override(t *testing.T) {
	r := NewRegistry()
	t1 := &stubTool{name: "bash"}
	t2 := &stubTool{name: "bash"}

	r.Register(t1)
	r.Register(t2) // should override

	all := r.All()
	if len(all) != 1 {
		t.Errorf("expected 1 tool after override, got %d", len(all))
	}
}

func TestRegistryFilter_AllNames(t *testing.T) {
	r := NewRegistry()
	r.Register(&stubTool{name: "bash"})
	r.Register(&stubTool{name: "read"})
	r.Register(&stubTool{name: "write"})

	// Filter with all names should return all tools
	filtered := r.Filter([]string{"bash", "read", "write"})
	if len(filtered.All()) != 3 {
		t.Errorf("expected 3 tools, got %d", len(filtered.All()))
	}
}

func TestRegistryFilter_IndependentFromOriginal(t *testing.T) {
	r := NewRegistry()
	r.Register(&stubTool{name: "bash"})
	r.Register(&stubTool{name: "read"})
	r.Register(&stubTool{name: "write"})

	filtered := r.Filter([]string{"bash"})

	// Adding to original should not affect filtered
	r.Register(&stubTool{name: "new_tool"})

	if _, ok := filtered.Get("new_tool"); ok {
		t.Error("filtered registry should not see tools added to original")
	}
}

func TestStubToolExecute(t *testing.T) {
	s := &stubTool{name: "test"}

	result, err := s.Execute(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Content != "ok" {
		t.Errorf("Content = %q, want 'ok'", result.Content)
	}
	if s.Description() != "test tool" {
		t.Errorf("Description() = %q, want 'test tool'", s.Description())
	}
}
