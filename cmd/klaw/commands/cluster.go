package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/eachlabs/klaw/internal/cluster"
	"github.com/eachlabs/klaw/internal/config"
	"github.com/spf13/cobra"
)

var (
	clusterDescription string
	clusterDisplayName string
)

func init() {
	// Cluster commands
	createCmd.AddCommand(createClusterCmd)
	getCmd.AddCommand(getClustersCmd)
	deleteCmd.AddCommand(deleteClusterCmd)
	describeCmd.AddCommand(describeClusterCmd)

	// Namespace commands
	createCmd.AddCommand(createNamespaceCmd)
	getCmd.AddCommand(getNamespacesCmd)
	deleteCmd.AddCommand(deleteNamespaceCmd)
}

// --- klaw create cluster ---

var createClusterCmd = &cobra.Command{
	Use:   "cluster <name>",
	Short: "Create a cluster",
	Long: `Create a new cluster for an organization or company.

A cluster is the top-level isolation boundary. Each company should have
its own cluster. A 'default' namespace is automatically created.

Examples:
  klaw create cluster acme-corp
  klaw create cluster acme-corp --description "Acme Corporation"`,
	Args: cobra.ExactArgs(1),
	RunE: runCreateCluster,
}

func init() {
	createClusterCmd.Flags().StringVar(&clusterDescription, "description", "", "cluster description")
	createClusterCmd.Flags().StringVar(&clusterDisplayName, "display-name", "", "display name")
}

func runCreateCluster(cmd *cobra.Command, args []string) error {
	name := args[0]

	store := cluster.NewStore(config.StateDir())
	ctxMgr := cluster.NewContextManager(config.ConfigDir())

	c := &cluster.Cluster{
		Name:        name,
		DisplayName: clusterDisplayName,
		Description: clusterDescription,
	}

	if err := store.CreateCluster(c); err != nil {
		return err
	}

	// Auto-switch to new cluster
	ctxMgr.SetCluster(name)

	fmt.Printf("Cluster '%s' created.\n", name)
	fmt.Printf("  Namespace 'default' created.\n")
	fmt.Printf("  Context switched to %s/default\n", name)
	fmt.Println("")
	fmt.Println("Next steps:")
	fmt.Printf("  Create namespaces: klaw create namespace marketing\n")
	fmt.Printf("  Add channels:      klaw create channel slack --name sales-bot\n")

	return nil
}

// --- klaw get clusters ---

var getClustersCmd = &cobra.Command{
	Use:     "clusters",
	Aliases: []string{"cluster", "cl"},
	Short:   "List clusters",
	RunE:    runGetClusters,
}

func runGetClusters(cmd *cobra.Command, args []string) error {
	store := cluster.NewStore(config.StateDir())
	ctxMgr := cluster.NewContextManager(config.ConfigDir())

	clusters, err := store.ListClusters()
	if err != nil {
		return err
	}

	if len(clusters) == 0 {
		fmt.Println("No clusters found.")
		fmt.Println("Create one with: klaw create cluster <name>")
		return nil
	}

	currentCluster, _, _ := ctxMgr.GetCurrent()

	if jsonOut {
		return json.NewEncoder(os.Stdout).Encode(clusters)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "  \tNAME\tNAMESPACES\tCHANNELS\tDESCRIPTION")
	for _, c := range clusters {
		marker := " "
		if c.Name == currentCluster {
			marker = "*"
		}

		namespaces, _ := store.ListNamespaces(c.Name)
		channels, _ := store.ListAllChannelBindings(c.Name)

		desc := c.Description
		if len(desc) > 40 {
			desc = desc[:37] + "..."
		}

		fmt.Fprintf(w, "%s\t%s\t%d\t%d\t%s\n",
			marker, c.Name, len(namespaces), len(channels), desc)
	}
	return w.Flush()
}

// --- klaw delete cluster ---

var deleteClusterCmd = &cobra.Command{
	Use:   "cluster <name>",
	Short: "Delete a cluster",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		store := cluster.NewStore(config.StateDir())

		if err := store.DeleteCluster(name); err != nil {
			return err
		}

		fmt.Printf("Cluster '%s' deleted.\n", name)
		return nil
	},
}

// --- klaw describe cluster ---

var describeClusterCmd = &cobra.Command{
	Use:   "cluster <name>",
	Short: "Show cluster details",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		store := cluster.NewStore(config.StateDir())

		c, err := store.GetCluster(name)
		if err != nil {
			return err
		}

		namespaces, _ := store.ListNamespaces(name)
		channels, _ := store.ListAllChannelBindings(name)

		if jsonOut {
			return json.NewEncoder(os.Stdout).Encode(c)
		}

		fmt.Printf("Name:        %s\n", c.Name)
		if c.DisplayName != "" {
			fmt.Printf("Display:     %s\n", c.DisplayName)
		}
		if c.Description != "" {
			fmt.Printf("Description: %s\n", c.Description)
		}
		fmt.Printf("Created:     %s\n", c.CreatedAt.Format("2006-01-02 15:04:05"))
		fmt.Println("")
		fmt.Printf("Namespaces (%d):\n", len(namespaces))
		for _, ns := range namespaces {
			bindings, _ := store.ListChannelBindings(name, ns.Name)
			fmt.Printf("  - %s (%d channels)\n", ns.Name, len(bindings))
		}
		fmt.Println("")
		fmt.Printf("Channels (%d):\n", len(channels))
		for _, ch := range channels {
			fmt.Printf("  - %s/%s [%s] %s\n", ch.Namespace, ch.Name, ch.Type, ch.Status)
		}

		return nil
	},
}

// --- klaw create namespace ---

var nsCluster string

var createNamespaceCmd = &cobra.Command{
	Use:     "namespace <name>",
	Aliases: []string{"ns"},
	Short:   "Create a namespace",
	Long: `Create a namespace within a cluster.

Namespaces organize teams or departments within a company.

Examples:
  klaw create namespace marketing
  klaw create namespace sales --description "Sales team"
  klaw create namespace engineering --cluster acme-corp`,
	Args: cobra.ExactArgs(1),
	RunE: runCreateNamespace,
}

func init() {
	createNamespaceCmd.Flags().StringVar(&nsCluster, "cluster", "", "cluster name (uses current if not set)")
	createNamespaceCmd.Flags().StringVar(&clusterDescription, "description", "", "namespace description")
}

func runCreateNamespace(cmd *cobra.Command, args []string) error {
	name := args[0]

	store := cluster.NewStore(config.StateDir())
	ctxMgr := cluster.NewContextManager(config.ConfigDir())

	clusterName := nsCluster
	if clusterName == "" {
		var err error
		clusterName, _, err = ctxMgr.RequireCurrent()
		if err != nil {
			return err
		}
	}

	ns := &cluster.Namespace{
		Name:        name,
		Cluster:     clusterName,
		Description: clusterDescription,
	}

	if err := store.CreateNamespace(ns); err != nil {
		return err
	}

	fmt.Printf("Namespace '%s' created in cluster '%s'.\n", name, clusterName)
	fmt.Println("")
	fmt.Printf("Switch to it: klaw config use-namespace %s\n", name)

	return nil
}

// --- klaw get namespaces ---

var getNamespacesCmd = &cobra.Command{
	Use:     "namespaces",
	Aliases: []string{"namespace", "ns"},
	Short:   "List namespaces",
	RunE:    runGetNamespaces,
}

func runGetNamespaces(cmd *cobra.Command, args []string) error {
	store := cluster.NewStore(config.StateDir())
	ctxMgr := cluster.NewContextManager(config.ConfigDir())

	clusterName, currentNS, err := ctxMgr.RequireCurrent()
	if err != nil {
		return err
	}

	namespaces, err := store.ListNamespaces(clusterName)
	if err != nil {
		return err
	}

	if len(namespaces) == 0 {
		fmt.Printf("No namespaces in cluster '%s'.\n", clusterName)
		return nil
	}

	if jsonOut {
		return json.NewEncoder(os.Stdout).Encode(namespaces)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(w, "  \tNAME\tCHANNELS\tAGENTS\tDESCRIPTION\n")
	for _, ns := range namespaces {
		marker := " "
		if ns.Name == currentNS {
			marker = "*"
		}

		channels, _ := store.ListChannelBindings(clusterName, ns.Name)

		desc := ns.Description
		if len(desc) > 30 {
			desc = desc[:27] + "..."
		}

		fmt.Fprintf(w, "%s\t%s\t%d\t%d\t%s\n",
			marker, ns.Name, len(channels), 0, desc)
	}
	return w.Flush()
}

// --- klaw delete namespace ---

var deleteNamespaceCmd = &cobra.Command{
	Use:     "namespace <name>",
	Aliases: []string{"ns"},
	Short:   "Delete a namespace",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]

		store := cluster.NewStore(config.StateDir())
		ctxMgr := cluster.NewContextManager(config.ConfigDir())

		clusterName, _, err := ctxMgr.RequireCurrent()
		if err != nil {
			return err
		}

		if err := store.DeleteNamespace(clusterName, name); err != nil {
			return err
		}

		fmt.Printf("Namespace '%s' deleted from cluster '%s'.\n", name, clusterName)
		return nil
	},
}
