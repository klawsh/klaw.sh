package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/eachlabs/klaw/internal/cluster"
	"github.com/eachlabs/klaw/internal/config"
	"github.com/eachlabs/klaw/internal/scheduler"
	"github.com/spf13/cobra"
)

var (
	cronSchedule string
	cronAgent    string
	cronTask     string
	cronChannel  string
)

var cronCmd = &cobra.Command{
	Use:   "cron",
	Short: "Manage scheduled tasks",
	Long: `Manage cron jobs for recurring agent tasks.

Use plain English to define schedules:
  "every day at 9am"
  "every monday at 10:30am"
  "every 30 minutes"
  "every hour"
  "daily"
  "weekly"

Examples:
  klaw cron create morning-report --schedule "every day at 9am" --agent reporter --task "Generate daily report"
  klaw cron list
  klaw cron run <job-id>
  klaw cron delete <job-id>`,
}

var cronCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a scheduled job",
	Long: `Create a new scheduled job with natural language scheduling.

Examples:
  klaw cron create daily-standup --schedule "every day at 9am" --agent standup-bot --task "Post standup reminder"
  klaw cron create weekly-report --schedule "every monday at 10am" --agent reporter --task "Generate weekly metrics"
  klaw cron create health-check --schedule "every 5 minutes" --agent monitor --task "Check system health"`,
	Args: cobra.ExactArgs(1),
	RunE: runCronCreate,
}

var cronListCmd = &cobra.Command{
	Use:   "list",
	Short: "List scheduled jobs",
	RunE:  runCronList,
}

var cronDeleteCmd = &cobra.Command{
	Use:   "delete <job-id>",
	Short: "Delete a scheduled job",
	Args:  cobra.ExactArgs(1),
	RunE:  runCronDelete,
}

var cronRunCmd = &cobra.Command{
	Use:   "run <job-id>",
	Short: "Run a job immediately",
	Args:  cobra.ExactArgs(1),
	RunE:  runCronRun,
}

var cronEnableCmd = &cobra.Command{
	Use:   "enable <job-id>",
	Short: "Enable a scheduled job",
	Args:  cobra.ExactArgs(1),
	RunE:  runCronEnable,
}

var cronDisableCmd = &cobra.Command{
	Use:   "disable <job-id>",
	Short: "Disable a scheduled job",
	Args:  cobra.ExactArgs(1),
	RunE:  runCronDisable,
}

var cronDescribeCmd = &cobra.Command{
	Use:   "describe <job-id>",
	Short: "Show job details",
	Args:  cobra.ExactArgs(1),
	RunE:  runCronDescribe,
}

var cronSetChannelCmd = &cobra.Command{
	Use:   "set-channel <job-id> <channel-id>",
	Short: "Set the Slack channel for a job to monitor",
	Args:  cobra.ExactArgs(2),
	RunE:  runCronSetChannel,
}

func init() {
	cronCreateCmd.Flags().StringVarP(&cronSchedule, "schedule", "s", "", "Schedule in plain English (required)")
	cronCreateCmd.Flags().StringVarP(&cronAgent, "agent", "a", "", "Agent to run the task (required)")
	cronCreateCmd.Flags().StringVarP(&cronTask, "task", "t", "", "Task/prompt for the agent (required)")
	cronCreateCmd.Flags().StringVarP(&cronChannel, "channel", "c", "", "Slack channel ID to read messages from (optional)")
	cronCreateCmd.MarkFlagRequired("schedule")
	cronCreateCmd.MarkFlagRequired("agent")
	cronCreateCmd.MarkFlagRequired("task")

	cronCmd.AddCommand(cronCreateCmd)
	cronCmd.AddCommand(cronListCmd)
	cronCmd.AddCommand(cronDeleteCmd)
	cronCmd.AddCommand(cronRunCmd)
	cronCmd.AddCommand(cronEnableCmd)
	cronCmd.AddCommand(cronDisableCmd)
	cronCmd.AddCommand(cronDescribeCmd)
	cronCmd.AddCommand(cronSetChannelCmd)
	rootCmd.AddCommand(cronCmd)
}

func getScheduler() *scheduler.Scheduler {
	s := scheduler.NewScheduler(config.StateDir() + "/scheduler")
	s.Load()
	return s
}

func runCronCreate(cmd *cobra.Command, args []string) error {
	name := args[0]

	ctxMgr := cluster.NewContextManager(config.ConfigDir())
	clusterName, namespace, err := ctxMgr.RequireCurrent()
	if err != nil {
		return err
	}

	// Validate agent exists
	store := cluster.NewStore(config.StateDir())
	if !store.AgentBindingExists(clusterName, namespace, cronAgent) {
		return fmt.Errorf("agent not found: %s\nCreate it with: klaw create agent %s --description \"...\"", cronAgent, cronAgent)
	}

	sched := getScheduler()

	// Parse and validate schedule
	cron, err := scheduler.ParseSchedule(cronSchedule)
	if err != nil {
		return err
	}

	job, err := sched.CreateJob(name, cronSchedule, cronAgent, cronTask, clusterName, namespace)
	if err != nil {
		return err
	}

	// Set channel config if provided
	if cronChannel != "" {
		if job.Config == nil {
			job.Config = make(map[string]string)
		}
		job.Config["channel"] = cronChannel
		sched.Save()
	}

	fmt.Println("✅ Scheduled job created!")
	fmt.Println()
	fmt.Printf("  ID:       %s\n", job.ID)
	fmt.Printf("  Name:     %s\n", job.Name)
	fmt.Printf("  Schedule: %s\n", job.Schedule)
	fmt.Printf("  Cron:     %s\n", cron)
	fmt.Printf("  Agent:    %s\n", job.Agent)
	fmt.Printf("  Task:     %s\n", truncateStr(job.Task, 50))
	if job.NextRun != nil {
		fmt.Printf("  Next Run: %s\n", job.NextRun.Format(time.RFC3339))
	}
	fmt.Println()
	fmt.Println("The job will run automatically when 'klaw start namespace' is running.")
	fmt.Printf("Run now: klaw cron run %s\n", job.ID)

	return nil
}

func runCronList(cmd *cobra.Command, args []string) error {
	ctxMgr := cluster.NewContextManager(config.ConfigDir())
	clusterName, namespace, err := ctxMgr.RequireCurrent()
	if err != nil {
		return err
	}

	sched := getScheduler()
	jobs := sched.ListJobs(clusterName, namespace)

	if len(jobs) == 0 {
		fmt.Printf("No scheduled jobs in %s/%s.\n", clusterName, namespace)
		fmt.Println()
		fmt.Println("Create one with:")
		fmt.Println("  klaw cron create daily-report --schedule \"every day at 9am\" --agent reporter --task \"Generate report\"")
		return nil
	}

	if jsonOut {
		return json.NewEncoder(os.Stdout).Encode(jobs)
	}

	fmt.Printf("Scheduled Jobs in %s/%s:\n\n", clusterName, namespace)

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tNAME\tSCHEDULE\tAGENT\tSTATUS\tNEXT RUN")
	fmt.Fprintln(w, "--\t----\t--------\t-----\t------\t--------")

	for _, job := range jobs {
		status := "enabled"
		if !job.Enabled {
			status = "disabled"
		}

		nextRun := "-"
		if job.NextRun != nil {
			nextRun = job.NextRun.Format("Jan 02 15:04")
		}

		scheduleDesc := scheduler.FormatSchedule(job.Cron)
		if len(scheduleDesc) > 25 {
			scheduleDesc = scheduleDesc[:22] + "..."
		}

		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
			job.ID, job.Name, scheduleDesc, job.Agent, status, nextRun)
	}
	w.Flush()

	return nil
}

func runCronDelete(cmd *cobra.Command, args []string) error {
	id := args[0]
	sched := getScheduler()

	if _, err := sched.GetJob(id); err != nil {
		return err
	}

	if err := sched.DeleteJob(id); err != nil {
		return err
	}

	fmt.Printf("✅ Deleted job: %s\n", id)
	return nil
}

func runCronRun(cmd *cobra.Command, args []string) error {
	id := args[0]
	sched := getScheduler()

	job, err := sched.GetJob(id)
	if err != nil {
		return err
	}

	fmt.Printf("🚀 Running job '%s' now...\n", job.Name)
	fmt.Printf("   Agent: %s\n", job.Agent)
	fmt.Printf("   Task: %s\n", job.Task)
	fmt.Println()
	fmt.Println("Note: For full execution, use 'klaw start namespace' which runs the scheduler.")

	return nil
}

func runCronEnable(cmd *cobra.Command, args []string) error {
	id := args[0]
	sched := getScheduler()

	if err := sched.EnableJob(id); err != nil {
		return err
	}

	job, _ := sched.GetJob(id)
	fmt.Printf("✅ Enabled job: %s\n", job.Name)
	if job.NextRun != nil {
		fmt.Printf("   Next run: %s\n", job.NextRun.Format(time.RFC3339))
	}
	return nil
}

func runCronDisable(cmd *cobra.Command, args []string) error {
	id := args[0]
	sched := getScheduler()

	if err := sched.DisableJob(id); err != nil {
		return err
	}

	fmt.Printf("✅ Disabled job: %s\n", id)
	return nil
}

func runCronSetChannel(cmd *cobra.Command, args []string) error {
	id := args[0]
	channelID := args[1]

	sched := getScheduler()
	job, err := sched.GetJob(id)
	if err != nil {
		return err
	}

	if job.Config == nil {
		job.Config = make(map[string]string)
	}
	job.Config["channel"] = channelID

	if err := sched.Save(); err != nil {
		return err
	}

	fmt.Printf("✅ Set channel for job '%s' to %s\n", job.Name, channelID)
	fmt.Println("The job will now read messages from this channel when it runs.")
	return nil
}

func runCronDescribe(cmd *cobra.Command, args []string) error {
	id := args[0]
	sched := getScheduler()

	job, err := sched.GetJob(id)
	if err != nil {
		return err
	}

	if jsonOut {
		return json.NewEncoder(os.Stdout).Encode(job)
	}

	status := "enabled"
	if !job.Enabled {
		status = "disabled"
	}

	fmt.Printf("ID:          %s\n", job.ID)
	fmt.Printf("Name:        %s\n", job.Name)
	fmt.Printf("Status:      %s\n", status)
	fmt.Printf("Schedule:    %s\n", job.Schedule)
	fmt.Printf("Cron:        %s\n", job.Cron)
	fmt.Printf("Readable:    %s\n", scheduler.FormatSchedule(job.Cron))
	fmt.Printf("Agent:       %s\n", job.Agent)
	if job.Config != nil && job.Config["channel"] != "" {
		fmt.Printf("Channel:     %s\n", job.Config["channel"])
	}
	fmt.Printf("Cluster:     %s\n", job.Cluster)
	fmt.Printf("Namespace:   %s\n", job.Namespace)
	fmt.Printf("Created:     %s\n", job.CreatedAt.Format(time.RFC3339))
	fmt.Printf("Run Count:   %d\n", job.RunCount)
	if job.LastRun != nil {
		fmt.Printf("Last Run:    %s\n", job.LastRun.Format(time.RFC3339))
	}
	if job.NextRun != nil {
		fmt.Printf("Next Run:    %s\n", job.NextRun.Format(time.RFC3339))
	}
	fmt.Println()
	fmt.Println("Task:")
	fmt.Println("---")
	fmt.Println(job.Task)

	if job.LastResult != "" {
		fmt.Println()
		fmt.Println("Last Result:")
		fmt.Println("---")
		fmt.Println(truncateStr(job.LastResult, 500))
	}

	if job.LastError != "" {
		fmt.Println()
		fmt.Printf("Last Error: %s\n", job.LastError)
	}

	return nil
}

// Helper for parsing schedule examples
func init() {
	// Add help examples
	cronCmd.Example = strings.TrimSpace(`
  # Create jobs with natural language schedules
  klaw cron create morning-standup --schedule "every day at 9am" --agent standup --task "Post standup reminder to #general"
  klaw cron create weekly-metrics --schedule "every monday at 10am" --agent analyst --task "Generate and post weekly metrics report"
  klaw cron create health-check --schedule "every 5 minutes" --agent monitor --task "Check all services and alert if any are down"
  klaw cron create backup-reminder --schedule "every friday at 5pm" --agent ops --task "Remind team to backup their work"

  # Schedule formats supported:
  #   "every minute"
  #   "every 5 minutes"
  #   "every hour"
  #   "every day at 9am"
  #   "every day at 14:30"
  #   "every monday at 10am"
  #   "every weekday at 9am"
  #   "daily"
  #   "weekly"
  #   "monthly"
`)
}
