package commands

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/eachlabs/klaw/internal/cluster"
	"github.com/eachlabs/klaw/internal/config"
	"github.com/eachlabs/klaw/internal/scheduler"
	"github.com/eachlabs/klaw/internal/tui"
	"github.com/spf13/cobra"
)

var dashboardCmd = &cobra.Command{
	Use:     "dashboard",
	Aliases: []string{"dash", "ui"},
	Short:   "Open the interactive dashboard",
	Long: `Open the interactive terminal dashboard for managing klaw.

The dashboard provides:
  📊 Overview      - Stats, agents, channels at a glance
  🤖 Agents        - Create, view, delete agents
  📡 Channels      - Manage Slack/Discord connections
  💬 Conversations - View message history
  ⚙️  Settings      - Cluster and orchestrator config

Navigation:
  1-5       Switch tabs
  Tab       Next tab
  ↑/↓ j/k   Navigate lists
  Enter     View details
  n         Create new agent
  d         Delete selected
  r         Refresh data
  q         Quit`,
	RunE: runDashboard,
}

func init() {
	rootCmd.AddCommand(dashboardCmd)
}

func runDashboard(cmd *cobra.Command, args []string) error {
	store := cluster.NewStore(config.StateDir())
	ctxMgr := cluster.NewContextManager(config.ConfigDir())

	clusterName, namespace, err := ctxMgr.RequireCurrent()
	if err != nil {
		return fmt.Errorf("no cluster selected: %w\nRun: klaw use cluster <name>", err)
	}

	// Create scheduler
	sched := scheduler.NewScheduler(config.StateDir() + "/scheduler")
	sched.Load()

	// Create and run dashboard
	m := tui.NewDashboard(store, sched, clusterName, namespace)
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())

	if _, err := p.Run(); err != nil {
		return fmt.Errorf("dashboard error: %w", err)
	}

	return nil
}
