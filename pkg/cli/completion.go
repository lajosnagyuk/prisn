// Package cli implements the prisn command-line interface.
package cli

import (
	"os"

	"github.com/spf13/cobra"
)

func newCompletionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "completion [bash|zsh|fish|powershell]",
		Short: "Generate shell completion scripts",
		Long: `Generate shell completion scripts for prisn.

To load completions:

Bash:
  $ source <(prisn completion bash)
  # To load completions for each session, execute once:
  # Linux:
  $ prisn completion bash > /etc/bash_completion.d/prisn
  # macOS:
  $ prisn completion bash > $(brew --prefix)/etc/bash_completion.d/prisn

Zsh:
  # If shell completion is not already enabled in your environment,
  # you will need to enable it. You can execute the following once:
  $ echo "autoload -U compinit; compinit" >> ~/.zshrc

  # To load completions for each session, execute once:
  $ prisn completion zsh > "${fpath[1]}/_prisn"

  # You will need to start a new shell for this setup to take effect.

Fish:
  $ prisn completion fish | source
  # To load completions for each session, execute once:
  $ prisn completion fish > ~/.config/fish/completions/prisn.fish

PowerShell:
  PS> prisn completion powershell | Out-String | Invoke-Expression
  # To load completions for each session, execute once:
  PS> prisn completion powershell > prisn.ps1
  # and source this file from your PowerShell profile.
`,
		DisableFlagsInUseLine: true,
		ValidArgs:             []string{"bash", "zsh", "fish", "powershell"},
		Args:                  cobra.MatchAll(cobra.ExactArgs(1), cobra.OnlyValidArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			switch args[0] {
			case "bash":
				return cmd.Root().GenBashCompletion(os.Stdout)
			case "zsh":
				return cmd.Root().GenZshCompletion(os.Stdout)
			case "fish":
				return cmd.Root().GenFishCompletion(os.Stdout, true)
			case "powershell":
				return cmd.Root().GenPowerShellCompletionWithDesc(os.Stdout)
			}
			return nil
		},
	}

	return cmd
}

// registerCompletions adds dynamic completion functions to commands.
// This is called from NewRootCmd to set up completions for resources, contexts, etc.
func registerCompletions(root *cobra.Command) {
	// Find commands that need dynamic completions
	for _, cmd := range root.Commands() {
		switch cmd.Name() {
		case "scale":
			// Complete deployment names for scale
			cmd.ValidArgsFunction = completeDeploymentNames
		case "logs":
			// Complete deployment/job names for logs
			cmd.ValidArgsFunction = completeDeploymentNames
		case "delete":
			// Delete has a subcommand structure, handle separately
			registerDeleteCompletions(cmd)
		case "get":
			// Complete resource types
			cmd.ValidArgsFunction = completeResourceTypes
		case "context":
			registerContextCompletions(cmd)
		}
	}
}

// completeDeploymentNames provides completion for deployment names.
func completeDeploymentNames(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) != 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	// Deployment names require server connection; disable file completion
	return nil, cobra.ShellCompDirectiveNoFileComp
}

// completeResourceTypes provides completion for resource types.
func completeResourceTypes(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) != 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	resources := []string{
		"deployments\tLong-running services and jobs",
		"deploy\tAlias for deployments",
		"d\tAlias for deployments",
		"apps\tPrisnApps (alias)",
		"jobs\tOne-time executions",
		"job\tAlias for jobs",
		"j\tAlias for jobs",
		"envs\tCached virtual environments",
		"env\tAlias for envs",
	}

	return resources, cobra.ShellCompDirectiveNoFileComp
}

// registerDeleteCompletions adds completions for the delete command.
func registerDeleteCompletions(deleteCmd *cobra.Command) {
	deleteCmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			// First arg: resource type
			return []string{
				"deploy\tDelete a deployment",
				"job\tDelete a job",
				"env\tDelete a cached environment",
			}, cobra.ShellCompDirectiveNoFileComp
		}
		// Second arg: resource name
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
}

// registerContextCompletions adds completions for context subcommands.
func registerContextCompletions(contextCmd *cobra.Command) {
	for _, sub := range contextCmd.Commands() {
		switch sub.Name() {
		case "use":
			sub.ValidArgsFunction = completeContextNames
		case "delete":
			sub.ValidArgsFunction = completeContextNames
		case "rename":
			sub.ValidArgsFunction = completeContextNames
		}
	}
}

// completeContextNames provides completion for context names.
func completeContextNames(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	// Static list for now; extend to load from config file
	return []string{
		"local\tLocal prisn server",
		"kubernetes\tKubernetes cluster",
	}, cobra.ShellCompDirectiveNoFileComp
}
