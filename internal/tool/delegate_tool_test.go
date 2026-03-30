package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestDelegateTool_Name(t *testing.T) {
	d := NewDelegateTool(nil, nil, NewRegistry())
	if d.Name() != "delegate" {
		t.Errorf("Name() = %q, want 'delegate'", d.Name())
	}
}

func TestDelegateTool_Schema(t *testing.T) {
	d := NewDelegateTool(nil, nil, NewRegistry())
	var schema map[string]interface{}
	if err := json.Unmarshal(d.Schema(), &schema); err != nil {
		t.Fatalf("Schema() returned invalid JSON: %v", err)
	}
	props, ok := schema["properties"].(map[string]interface{})
	if !ok {
		t.Fatal("schema missing properties")
	}
	if _, ok := props["task"]; !ok {
		t.Error("schema missing 'task' property")
	}
}

func TestDelegateTool_MissingTask(t *testing.T) {
	d := NewDelegateTool(nil, nil, NewRegistry())
	result, err := d.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error for missing task")
	}
	if !strings.Contains(result.Content, "task parameter is required") {
		t.Errorf("unexpected content: %q", result.Content)
	}
}

func TestDelegateTool_InvalidParams(t *testing.T) {
	d := NewDelegateTool(nil, nil, NewRegistry())
	result, err := d.Execute(context.Background(), json.RawMessage(`not json`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error for invalid params")
	}
	if !strings.Contains(result.Content, "invalid params") {
		t.Errorf("unexpected content: %q", result.Content)
	}
}

func TestDelegateTool_DepthLimit(t *testing.T) {
	d := &DelegateTool{
		depth:    3,
		maxDepth: 3,
		tools:    NewRegistry(),
	}
	result, err := d.Execute(context.Background(), json.RawMessage(`{"task":"do something"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error at max depth")
	}
	if !strings.Contains(result.Content, "maximum delegation depth") {
		t.Errorf("unexpected content: %q", result.Content)
	}
}

func TestDelegateTool_SuccessfulDelegation(t *testing.T) {
	runCalled := false
	runFn := func(ctx context.Context, cfg RunConfig) (string, error) {
		runCalled = true
		if cfg.Prompt != "do the thing" {
			t.Errorf("unexpected prompt: %q", cfg.Prompt)
		}
		return "sub-agent result", nil
	}

	reg := NewRegistry()
	reg.Register(&stubTool{name: "bash"})
	reg.Register(&stubTool{name: "read"})

	d := NewDelegateTool(runFn, "mock-provider", reg)

	result, err := d.Execute(context.Background(), json.RawMessage(`{"task":"do the thing"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !runCalled {
		t.Error("runFn was not called")
	}
	if result.IsError {
		t.Errorf("unexpected error result: %s", result.Content)
	}
	if result.Content != "sub-agent result" {
		t.Errorf("Content = %q, want 'sub-agent result'", result.Content)
	}
}

func TestDelegateTool_OutputTruncation(t *testing.T) {
	longOutput := strings.Repeat("x", 35000)
	runFn := func(ctx context.Context, cfg RunConfig) (string, error) {
		return longOutput, nil
	}

	d := NewDelegateTool(runFn, nil, NewRegistry())
	result, err := d.Execute(context.Background(), json.RawMessage(`{"task":"generate lots"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Content) > 31000 {
		t.Errorf("output should be truncated, got %d chars", len(result.Content))
	}
	if !strings.HasSuffix(result.Content, "... (output truncated)") {
		t.Error("truncated output should end with truncation marker")
	}
}

func TestDelegateTool_ToolAllowlist(t *testing.T) {
	var receivedTools *Registry
	runFn := func(ctx context.Context, cfg RunConfig) (string, error) {
		receivedTools = cfg.Tools
		return "ok", nil
	}

	reg := NewRegistry()
	reg.Register(&stubTool{name: "bash"})
	reg.Register(&stubTool{name: "read"})
	reg.Register(&stubTool{name: "write"})

	d := NewDelegateTool(runFn, nil, reg)
	_, err := d.Execute(context.Background(), json.RawMessage(`{"task":"test","tools":["bash","read"]}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	names := receivedTools.Names()
	// Should have bash, read, and delegate (child)
	if len(names) != 3 {
		t.Errorf("expected 3 tools, got %d: %v", len(names), names)
	}
	if _, ok := receivedTools.Get("bash"); !ok {
		t.Error("expected bash in child tools")
	}
	if _, ok := receivedTools.Get("read"); !ok {
		t.Error("expected read in child tools")
	}
	if _, ok := receivedTools.Get("write"); ok {
		t.Error("write should not be in child tools with allowlist")
	}
}

func TestDelegateTool_ExcludesParentDelegate(t *testing.T) {
	var receivedTools *Registry
	runFn := func(ctx context.Context, cfg RunConfig) (string, error) {
		receivedTools = cfg.Tools
		return "ok", nil
	}

	reg := NewRegistry()
	reg.Register(&stubTool{name: "bash"})

	d := NewDelegateTool(runFn, nil, reg)
	// Register the delegate itself in the parent registry
	reg.Register(d)

	_, err := d.Execute(context.Background(), json.RawMessage(`{"task":"test"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Child should have bash + child delegate (not parent delegate)
	childDelegate, ok := receivedTools.Get("delegate")
	if !ok {
		t.Fatal("child should have a delegate tool")
	}
	childDT, ok := childDelegate.(*DelegateTool)
	if !ok {
		t.Fatal("delegate should be *DelegateTool")
	}
	if childDT.depth != 1 {
		t.Errorf("child delegate depth = %d, want 1", childDT.depth)
	}
}

func TestDelegateTool_NoChildDelegateAtMaxDepth(t *testing.T) {
	var receivedTools *Registry
	runFn := func(ctx context.Context, cfg RunConfig) (string, error) {
		receivedTools = cfg.Tools
		return "ok", nil
	}

	reg := NewRegistry()
	reg.Register(&stubTool{name: "bash"})

	d := &DelegateTool{
		runFn:    runFn,
		tools:    reg,
		depth:    2,
		maxDepth: 3,
	}

	_, err := d.Execute(context.Background(), json.RawMessage(`{"task":"test"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// At depth 2, child would be depth 3 == maxDepth, so no delegate should be added
	if _, ok := receivedTools.Get("delegate"); ok {
		t.Error("child at max depth should not have delegate tool")
	}
}

func TestDelegateTool_RunFnError(t *testing.T) {
	runFn := func(ctx context.Context, cfg RunConfig) (string, error) {
		return "", context.DeadlineExceeded
	}

	d := NewDelegateTool(runFn, nil, NewRegistry())
	result, err := d.Execute(context.Background(), json.RawMessage(`{"task":"fail"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error result")
	}
	if !strings.Contains(result.Content, "sub-agent error") {
		t.Errorf("unexpected content: %q", result.Content)
	}
}

func TestDelegateTool_CustomSystemPrompt(t *testing.T) {
	var receivedPrompt string
	runFn := func(ctx context.Context, cfg RunConfig) (string, error) {
		receivedPrompt = cfg.SystemPrompt
		return "ok", nil
	}

	d := NewDelegateTool(runFn, nil, NewRegistry())
	_, err := d.Execute(context.Background(), json.RawMessage(`{"task":"test","system_prompt":"You are a code reviewer."}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if receivedPrompt != "You are a code reviewer." {
		t.Errorf("system_prompt = %q, want 'You are a code reviewer.'", receivedPrompt)
	}
}

func TestDelegateTool_CustomMaxTokens(t *testing.T) {
	var receivedMaxTokens int
	runFn := func(ctx context.Context, cfg RunConfig) (string, error) {
		receivedMaxTokens = cfg.MaxTokens
		return "ok", nil
	}

	d := NewDelegateTool(runFn, nil, NewRegistry())
	_, err := d.Execute(context.Background(), json.RawMessage(`{"task":"test","max_tokens":4096}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if receivedMaxTokens != 4096 {
		t.Errorf("max_tokens = %d, want 4096", receivedMaxTokens)
	}
}
