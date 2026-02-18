package cli

import (
	gocontext "context"
	"fmt"
	"os"
	"time"

	"github.com/lajosnagyuk/prisn/pkg/client"
	"github.com/lajosnagyuk/prisn/pkg/context"
	"github.com/spf13/cobra"
)

func newClusterCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cluster",
		Short: "Manage cluster membership and status",
		Long: `Commands for managing the prisn cluster.

prisn uses Raft consensus for metadata operations (deployments, scaling, etc.)
while keeping the hot path (execution) local and fast.

The target server is determined by the current context. Use --context to override.

Examples:
  prisn cluster status              # Show cluster status
  prisn cluster nodes               # List cluster nodes
  prisn cluster join <addr>         # Join an existing cluster
  prisn cluster leave               # Gracefully leave the cluster
  prisn -c prod cluster status      # Status of prod cluster`,
	}

	cmd.AddCommand(
		newClusterStatusCmd(),
		newClusterNodesCmd(),
		newClusterJoinCmd(),
		newClusterLeaveCmd(),
	)

	return cmd
}

// getClient resolves the context and creates a client for cluster commands.
func getClient(cmd *cobra.Command) (*client.Client, error) {
	flagContext, _ := cmd.Root().PersistentFlags().GetString("context")
	flagNamespace, _ := cmd.Root().PersistentFlags().GetString("namespace")

	resolved, err := context.Resolve(flagContext, flagNamespace)
	if err != nil {
		return nil, err
	}

	return client.New(resolved)
}

func newClusterStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show cluster status",
		Long: `Show the status of the prisn cluster including:
- Current node state (leader, follower, candidate)
- Leader identity
- Node count
- Deployment count
- Raft term and commit index

In Kubernetes mode, shows CRD counts and operator status.

Uses the current context. Override with --context flag.

Examples:
  prisn cluster status
  prisn -c prod cluster status`,

		RunE: func(cmd *cobra.Command, args []string) error {
			// Resolve context to check mode
			flagContext, _ := cmd.Root().PersistentFlags().GetString("context")
			flagNamespace, _ := cmd.Root().PersistentFlags().GetString("namespace")
			resolved, err := context.Resolve(flagContext, flagNamespace)
			if err != nil {
				return err
			}

			if resolved.IsKubernetes() {
				// Kubernetes mode - show K8s cluster status
				return showKubernetesStatus(resolved)
			}

			// Remote/local mode - use HTTP client
			c, err := getClient(cmd)
			if err != nil {
				return err
			}

			var status map[string]any
			if err := c.GetJSON("/api/v1/cluster/status", &status); err != nil {
				if client.IsOffline(err) {
					client.HandleOfflineError(err)
					return nil
				}
				return err
			}

			// Pretty print
			fmt.Println("CLUSTER STATUS")
			fmt.Println("==============")
			fmt.Println()
			fmt.Printf("Context:     %s\n", c.Context().Name)
			fmt.Printf("Server:      %s\n", c.Context().Server)
			fmt.Println()
			fmt.Printf("Node ID:     %s\n", status["node_id"])
			fmt.Printf("State:       %s\n", status["state"])
			fmt.Printf("Leader:      %s\n", status["leader"])
			fmt.Printf("Term:        %.0f\n", status["term"])
			fmt.Println()
			fmt.Printf("Nodes:       %.0f\n", status["nodes"])
			fmt.Printf("Deployments: %.0f\n", status["deployments"])

			return nil
		},
	}

	return cmd
}

// showKubernetesStatus displays cluster status for Kubernetes mode.
func showKubernetesStatus(resolved *context.Resolved) error {
	fmt.Println("KUBERNETES CLUSTER STATUS")
	fmt.Println("=========================")
	fmt.Println()
	fmt.Printf("Context:     %s\n", resolved.Name)
	fmt.Printf("Mode:        kubernetes\n")
	kubeCtx := resolved.KubeContext
	if kubeCtx == "" {
		kubeCtx = "(current kubeconfig context)"
	}
	fmt.Printf("KubeContext: %s\n", kubeCtx)
	fmt.Printf("Namespace:   %s\n", resolved.Namespace)
	fmt.Println()

	// List CRD counts
	apps, err := ListAppsInKubernetes(gocontext.Background(), resolved)
	if err != nil {
		fmt.Printf("PrisnApps:   (error: %v)\n", err)
	} else {
		runningCount := 0
		for _, app := range apps {
			if app.Phase == "Running" {
				runningCount++
			}
		}
		fmt.Printf("PrisnApps:   %d (%d running)\n", len(apps), runningCount)
	}

	fmt.Println()
	fmt.Println("Use 'prisn get apps' to see detailed app list")

	return nil
}

func newClusterNodesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "nodes",
		Short: "List cluster nodes",
		Long: `List all nodes in the prisn cluster.

Shows node ID, address, state, and health status for each node.
Uses the current context. Override with --context flag.

Examples:
  prisn cluster nodes
  prisn -c prod cluster nodes`,

		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := getClient(cmd)
			if err != nil {
				return err
			}

			var result struct {
				Items []struct {
					ID       string    `json:"id"`
					Address  string    `json:"address"`
					State    string    `json:"state"`
					JoinedAt time.Time `json:"joined_at"`
					LastSeen time.Time `json:"last_seen"`
				} `json:"items"`
				Count int `json:"count"`
			}

			if err := c.GetJSON("/api/v1/cluster/nodes", &result); err != nil {
				if client.IsOffline(err) {
					client.HandleOfflineError(err)
					return nil
				}
				return err
			}

			if result.Count == 0 {
				fmt.Printf("Context: %s (%s)\n\n", c.Context().Name, c.Context().Server)
				fmt.Println("No nodes in cluster (single-node mode)")
				return nil
			}

			fmt.Println("CLUSTER NODES")
			fmt.Println("=============")
			fmt.Printf("Context: %s (%s)\n\n", c.Context().Name, c.Context().Server)
			fmt.Printf("%-20s %-25s %-12s %-20s\n", "NODE ID", "ADDRESS", "STATE", "LAST SEEN")
			fmt.Printf("%-20s %-25s %-12s %-20s\n", "-------", "-------", "-----", "---------")

			for _, node := range result.Items {
				lastSeen := "never"
				if !node.LastSeen.IsZero() {
					lastSeen = formatAge(time.Since(node.LastSeen))
				}
				fmt.Printf("%-20s %-25s %-12s %-20s\n",
					node.ID,
					node.Address,
					node.State,
					lastSeen)
			}

			return nil
		},
	}

	return cmd
}

func newClusterJoinCmd() *cobra.Command {
	var (
		nodeID  string
		address string
	)

	cmd := &cobra.Command{
		Use:   "join <leader-address>",
		Short: "Join an existing cluster",
		Long: `Join this node to an existing prisn cluster.

The leader address should be the address of the current cluster leader.
If not the leader, the request will be forwarded.

Note: This command connects directly to the leader address provided,
not the current context's server.

Examples:
  prisn cluster join leader.example.com:7331
  prisn cluster join --node-id worker-1 --address worker-1.example.com:7332 leader.example.com:7331`,

		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			leaderAddr := args[0]

			// If no node ID provided, use hostname
			if nodeID == "" {
				hostname, _ := os.Hostname()
				nodeID = hostname
			}

			// If no address provided, we can't join
			if address == "" {
				return fmt.Errorf("--address is required (your node's address for cluster communication)")
			}

			// Create a temporary context for the leader
			leaderCtx := &context.Resolved{
				Name:      "leader",
				Server:    leaderAddr,
				Namespace: "default",
				Source:    "join command",
			}

			c, err := client.New(leaderCtx)
			if err != nil {
				return err
			}

			reqBody := map[string]string{
				"node_id": nodeID,
				"address": address,
			}

			var result map[string]any
			if err := c.PostJSON("/api/v1/cluster/join", reqBody, &result); err != nil {
				if client.IsOffline(err) {
					return fmt.Errorf("cannot connect to leader at %s: %w", leaderAddr, err)
				}
				return fmt.Errorf("join failed: %w", err)
			}

			fmt.Printf("Successfully joined cluster!\n")
			fmt.Printf("  Node ID: %s\n", nodeID)
			fmt.Printf("  Address: %s\n", address)
			fmt.Printf("  Leader:  %s\n", leaderAddr)

			return nil
		},
	}

	cmd.Flags().StringVar(&nodeID, "node-id", "", "Node ID for this node (default: hostname)")
	cmd.Flags().StringVar(&address, "address", "", "This node's address (required)")

	return cmd
}

func newClusterLeaveCmd() *cobra.Command {
	var (
		nodeID string
		yes    bool
	)

	cmd := &cobra.Command{
		Use:   "leave",
		Short: "Leave the cluster",
		Long: `Gracefully leave the prisn cluster.

This will remove this node from cluster membership and stop replicating data.
Any deployments assigned to this node will be reassigned to other nodes.

Uses the current context. Override with --context flag.

Examples:
  prisn cluster leave
  prisn cluster leave --yes            # Skip confirmation
  prisn cluster leave --node-id worker-1  # Remove a specific node
  prisn -c prod cluster leave          # Leave prod cluster`,

		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := getClient(cmd)
			if err != nil {
				return err
			}

			if !yes {
				fmt.Printf("Leave cluster at %s? [y/N] ", c.Context().Server)
				var response string
				fmt.Scanln(&response)
				if response != "y" && response != "yes" {
					fmt.Println("Cancelled")
					return nil
				}
			}

			reqBody := map[string]string{
				"node_id": nodeID, // Empty means leave self
			}

			var result map[string]any
			if err := c.PostJSON("/api/v1/cluster/leave", reqBody, &result); err != nil {
				if client.IsOffline(err) {
					client.HandleOfflineError(err)
					return nil
				}
				return fmt.Errorf("leave failed: %w", err)
			}

			fmt.Println("Successfully left cluster")

			return nil
		},
	}

	cmd.Flags().StringVar(&nodeID, "node-id", "", "Node to remove (default: this node)")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Skip confirmation")

	return cmd
}

// formatAge formats a duration as a human-readable age.
func formatAge(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

// formatStatus formats a deployment status phase for display.
func formatStatus(phase string) string {
	switch phase {
	case "Running":
		return "Running"
	case "Starting":
		return "Starting"
	case "Pending":
		return "Pending"
	case "Failed":
		return "Failed"
	case "CrashLoopBackOff":
		return "CrashLoop"
	case "Stopped":
		return "Stopped"
	case "Paused":
		return "Paused"
	default:
		if phase == "" {
			return "Unknown"
		}
		return phase
	}
}
