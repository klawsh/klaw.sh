package commands

import (
	"fmt"

	"github.com/spf13/cobra"
)

var (
	cfgFile string
	verbose bool
	jsonOut bool
)

var rootCmd = &cobra.Command{
	Use:   "klaw",
	Short: "klaw - lightweight AI assistant",
	Long: `klaw is a minimal, fast, single-binary AI assistant.

kubectl-style commands for AI:
  klaw chat              Interactive terminal chat
  klaw get <resource>    List resources
  klaw create <resource> Create a resource
  klaw delete <resource> Delete a resource
  klaw describe <resource> Show resource details
  klaw config            Manage configuration`,
}

func init() {
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default: ~/.klaw/config.toml)")
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "verbose output")
	rootCmd.PersistentFlags().BoolVar(&jsonOut, "json", false, "output as JSON")

	// Add commands
	rootCmd.AddCommand(chatCmd)
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(getCmd)
	rootCmd.AddCommand(createCmd)
	rootCmd.AddCommand(deleteCmd)
	rootCmd.AddCommand(describeCmd)
	rootCmd.AddCommand(configCmd)
	rootCmd.AddCommand(initCmd)
}

func Execute(ver string) error {
	version = ver
	return rootCmd.Execute()
}

var version string

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("klaw %s\n", version)
	},
}
