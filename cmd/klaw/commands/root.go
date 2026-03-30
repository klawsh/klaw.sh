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
	Short: "klaw - Creative Agent Runtime",
	Long: `klaw is a stateless creative agent runtime for each::labs.

  klaw api start         Start the HTTP API server
  klaw api-key create    Create an API key
  klaw chat              Interactive terminal chat
  klaw upgrade           Self-update to latest version
  klaw config            Manage configuration`,
}

func init() {
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default: ~/.klaw/config.toml)")
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "verbose output")
	rootCmd.PersistentFlags().BoolVar(&jsonOut, "json", false, "output as JSON")

	rootCmd.AddCommand(chatCmd)
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(configCmd)
	rootCmd.AddCommand(initCmd)
	rootCmd.AddCommand(upgradeCmd)
	rootCmd.AddCommand(apiCmd)
	rootCmd.AddCommand(apiKeyCmd)
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
