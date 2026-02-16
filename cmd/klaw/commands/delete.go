package commands

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/eachlabs/klaw/internal/cluster"
	"github.com/eachlabs/klaw/internal/config"
	"github.com/spf13/cobra"
)

var deleteCmd = &cobra.Command{
	Use:     "delete <resource> [name]",
	Aliases: []string{"rm"},
	Short:   "Delete a resource",
	Long: `Delete a resource.

Resources:
  server     Stop a running server
  session    Delete a session
  channel    Remove a channel configuration`,
}

func init() {
	deleteCmd.AddCommand(deleteServerCmd)
	deleteCmd.AddCommand(deleteSessionCmd)
	deleteCmd.AddCommand(deleteChannelCmd)
}

var deleteServerCmd = &cobra.Command{
	Use:     "server",
	Aliases: []string{"srv"},
	Short:   "Stop a running server",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("No servers running.")
		return nil
	},
}

var deleteSessionCmd = &cobra.Command{
	Use:     "session <id>",
	Aliases: []string{"sess"},
	Short:   "Delete a session",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		sessionID := args[0]
		sessionPath := filepath.Join(config.SessionsDir(), sessionID+".json")

		if _, err := os.Stat(sessionPath); os.IsNotExist(err) {
			return fmt.Errorf("session not found: %s", sessionID)
		}

		if err := os.Remove(sessionPath); err != nil {
			return fmt.Errorf("failed to delete session: %w", err)
		}

		fmt.Printf("Deleted session: %s\n", sessionID)
		return nil
	},
}

var deleteChannelCmd = &cobra.Command{
	Use:     "channel <name>",
	Aliases: []string{"ch"},
	Short:   "Remove a channel from the current namespace",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]

		store := cluster.NewStore(config.StateDir())
		ctxMgr := cluster.NewContextManager(config.ConfigDir())

		clusterName, namespace, err := ctxMgr.RequireCurrent()
		if err != nil {
			return err
		}

		if err := store.DeleteChannelBinding(clusterName, namespace, name); err != nil {
			return err
		}

		fmt.Printf("Channel '%s' deleted from %s/%s.\n", name, clusterName, namespace)
		return nil
	},
}
