package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/eachlabs/klaw/internal/channel"
	"github.com/eachlabs/klaw/internal/provider"
)

// ApprovalConfig controls which tools require user approval.
type ApprovalConfig struct {
	Enabled         bool
	RequireApproval []string // tool names needing approval, e.g. ["bash", "write"]
}

// NeedsApproval returns true if the named tool requires user approval.
func (ac *ApprovalConfig) NeedsApproval(toolName string) bool {
	if !ac.Enabled {
		return false
	}
	for _, name := range ac.RequireApproval {
		if name == toolName {
			return true
		}
	}
	return false
}

// RequestApproval sends an approval prompt to the channel and waits for response.
func RequestApproval(ctx context.Context, ch channel.Channel, tc provider.ToolCall) (bool, error) {
	// Show what we're asking approval for
	prompt := fmt.Sprintf("\n⚠ Tool '%s' requires approval. Execute? [y/N]: ", tc.Name)
	ch.Send(ctx, &channel.Message{
		Role:    "assistant",
		Content: prompt,
	})

	// Wait for user response
	select {
	case <-ctx.Done():
		return false, ctx.Err()
	case msg, ok := <-ch.Receive():
		if !ok {
			return false, fmt.Errorf("channel closed")
		}
		response := strings.TrimSpace(strings.ToLower(msg.Content))
		return response == "y" || response == "yes", nil
	}
}
