# CLI Reference

**All prisn commands and flags**

## Global Flags

```bash
prisn [command] [flags]

  -c, --context string     Use a specific context
  -n, --namespace string   Target namespace (default: from context)
  -o, --output string      Output format: text, json, yaml (default: text)
  -v, --verbose            Verbose output
```

## Running Code

### run

Execute a script once and exit.

```bash
prisn run <script> [args...] [flags]
```

**Flags:**
```
  -e, --env strings        Environment variables (KEY=value)
      --runtime string     Override runtime detection
      --timeout duration   Execution timeout (default: 10m)
      --detach             Don't wait for completion
      --workdir string     Working directory
```

**Examples:**
```bash
prisn run script.py
prisn run script.py -- --arg1 value
prisn run script.py -e API_KEY=xxx -e DEBUG=true
prisn run script.py --timeout 5m
prisn run script.py --detach
```

### dev

Watch for changes and re-run a script automatically.

```bash
prisn dev <script> [flags]
```

**Flags:**
```
  -e, --env strings        Environment variables (KEY=value)
  -w, --workdir string     Working directory
      --debounce duration  Wait time after changes (default: 300ms)
      --clear              Clear screen between runs
```

**Examples:**
```bash
prisn dev script.py               # Watch and run
prisn dev api.py --clear          # Clear screen between runs
prisn dev test.py --debounce 1s   # Wait 1s after changes
prisn dev script.py -e DEBUG=true # With env vars
```

## Deployments

### deploy

Deploy a service, job, or cron job.

```bash
prisn deploy <script> [flags]
```

**Flags:**
```
      --name string        Deployment name (default: script name)
      --port int           Port to expose (makes it a service)
      --replicas int       Number of replicas (default: 1)
      --schedule string    Cron schedule (makes it a cron job)
  -e, --env strings        Environment variables
      --runtime string     Override runtime
      --wait               Wait for deployment to be ready
      --wait-timeout       Timeout for --wait (default: 5m)
  -l, --label strings      Labels (key=value)
```

**Examples:**
```bash
prisn deploy api.py --port 8080
prisn deploy api.py --port 8080 --replicas 3
prisn deploy worker.py --replicas 5 -e QUEUE=high
prisn deploy backup.sh --schedule "0 3 * * *"
prisn deploy api.py --port 8080 --wait
```

### apply

Apply configuration from file.

```bash
prisn apply [flags]
```

**Flags:**
```
  -f, --file string        Config file (default: prisn.toml)
      --dry-run            Show what would change
      --prune              Delete resources not in config
```

**Examples:**
```bash
prisn apply -f prisn.toml
prisn apply -f prisn.prod.toml
prisn apply -f prisn.toml --dry-run
prisn apply -f deployment.yaml
```

### scale

Change replica count.

```bash
prisn scale <name> <replicas> [flags]
```

**Flags:**
```
      --dry-run            Show what would be done
```

The replicas argument is smart - it understands what you mean:

**Examples:**
```bash
prisn scale api 5              # Set to exactly 5 replicas
prisn scale api 0              # Pause (scale to 0)
prisn scale api +2             # Add 2 replicas
prisn scale api -- -1          # Remove 1 replica (use -- before negative)
prisn scale api x1.5           # Multiply by 1.5
prisn scale api 150%           # Scale to 150% of current
prisn scale api auto           # Auto-scale based on load
prisn scale api x2 --dry-run   # Preview scaling
```

### delete

Delete a resource.

```bash
prisn delete <type> <name> [flags]
prisn delete -f <file>
```

**Types:** `service`, `job`, `cronjob`, `secret`, `deploy` (alias)

**Flags:**
```
  -f, --file string        Delete resources from file
      --force              Skip confirmation
      --all                Delete all in namespace
```

**Examples:**
```bash
prisn delete service api
prisn delete cronjob backup
prisn delete -f prisn.toml
prisn delete --all --force
```

## Viewing Resources

### get

List resources.

```bash
prisn get <type> [name] [flags]
```

**Types:** `deploy`, `service`, `job`, `cronjob`, `secret`, `all`

**Flags:**
```
  -o, --output string      Format: text, json, yaml, wide
  -l, --label string       Filter by label
      --all-namespaces     Show all namespaces
```

**Examples:**
```bash
prisn get deploy
prisn get deploy api
prisn get deploy -o yaml
prisn get all
prisn get cronjob
```

### status

Show deployment status.

```bash
prisn status [name] [flags]
```

**Examples:**
```bash
prisn status
prisn status api
prisn status -o json
```

### logs

View logs.

```bash
prisn logs <name> [flags]
```

**Flags:**
```
  -f, --follow             Stream logs
      --tail int           Lines from end (default: 100)
      --since duration     Logs since (e.g., 1h, 30m)
  -t, --timestamps         Show timestamps
      --all                All replicas
```

**Examples:**
```bash
prisn logs api
prisn logs api -f
prisn logs api --tail 50
prisn logs api --since 1h
```

### describe

Show detailed information about a resource.

```bash
prisn describe <type> <name> [flags]
```

**Examples:**
```bash
prisn describe deploy api
prisn describe deploy api -o yaml
```

Shows comprehensive details including:
- Configuration (runtime, source, args, env)
- Status (phase, replicas, restarts, uptime)
- Health checks and history
- Recent events and logs

### exec

Execute a command in a deployment's container/process.

```bash
prisn exec <deployment> -- <command> [args...]
```

**Flags:**
```
  -i, --stdin              Pass stdin to container
  -t, --tty                Allocate a TTY
```

**Examples:**
```bash
prisn exec api -- ls -la
prisn exec api -- python -c "import sys; print(sys.path)"
prisn exec api -it -- /bin/sh
```

### diff

Show changes between local config and deployed state.

```bash
prisn diff <name> [flags]
prisn diff -f <file>
```

**Flags:**
```
  -f, --file string        Compare with config file
```

**Examples:**
```bash
prisn diff api                    # Compare current vs deployed
prisn diff -f prisn.toml          # Compare file vs deployed
prisn diff api -o yaml            # Output as YAML diff
```

### top

Live resource usage display (TUI).

```bash
prisn top [flags]
```

**Flags:**
```
      --sort string        Sort by: cpu, memory, name (default: cpu)
      --interval duration  Refresh interval (default: 2s)
```

**Examples:**
```bash
prisn top                    # Live resource usage
prisn top --sort memory      # Sort by memory
prisn top --interval 5s      # Update every 5s
```

Shows real-time CPU, memory, and request metrics for all deployments.

### history

View deployment history.

```bash
prisn history <name> [flags]
```

**Examples:**
```bash
prisn history api
prisn history api -o json
```

### rollback

Rollback a deployment to a previous version.

```bash
prisn rollback <name> [revision] [flags]
```

**Flags:**
```
      --to-revision int    Specific revision to rollback to
      --dry-run            Show what would change without applying
```

**Examples:**
```bash
prisn rollback api               # Rollback to previous version
prisn rollback api 3             # Rollback to revision 3
prisn rollback api --dry-run     # Preview rollback
```

### events

View audit events.

```bash
prisn events [flags]
```

**Flags:**
```
      --limit int          Max events (default: 20)
      --resource string    Filter by resource
      --action string      Filter by action
```

### errors

View error summary.

```bash
prisn errors [flags]
```

**Flags:**
```
      --limit int          Max errors (default: 10)
      --since duration     Since (default: 24h)
```

## Secrets & Environment

### env

Manage environment variables.

```bash
prisn env <command> [args]
```

**Subcommands:**
```
  set <name> KEY=value...    Set env vars for deployment
  get <name>                 Show env vars
  unset <name> KEY...        Remove env vars
```

**Examples:**
```bash
prisn env set api LOG_LEVEL=debug
prisn env get api
prisn env unset api DEBUG
```

### secret

Manage secrets.

```bash
prisn secret <command> [args]
```

**Subcommands:**
```
  create <name> [flags]      Create a secret
  list                       List secrets
  get <name>                 Get secret (shows keys only)
  delete <name>              Delete secret
```

**Flags for create:**
```
      --from-literal KEY=val    Add key-value pair
      --from-file KEY=path      Add from file
      --from-env-file path      Add from .env file
```

**Examples:**
```bash
prisn secret create db-creds --from-literal url=postgres://...
prisn secret create certs --from-file tls.crt=./cert.pem
prisn secret create app-env --from-env-file .env.prod
prisn secret list
prisn secret delete db-creds
```

## Tokens (RBAC)

### token

Manage API tokens.

```bash
prisn token <command> [args]
```

**Subcommands:**
```
  create [flags]             Create token
  list [flags]               List tokens
  get <id>                   Get token details
  revoke <id>                Revoke token
  delete <id>                Delete token
```

**Flags for create:**
```
      --name string          Token name (required)
  -r, --role string          Role: admin, developer, viewer (required)
      --namespace strings    Namespaces to access (use * for all)
      --expires-in string    Expiration (e.g., 24h, 7d, 30d)
```

**Examples:**
```bash
prisn token create --name "CI/CD" --role developer --namespace default
prisn token create --name "Admin" --role admin --namespace "*"
prisn token create --name "Temp" --role viewer --namespace staging --expires-in 7d
prisn token list
prisn token list --role admin
prisn token revoke abc123
prisn token delete abc123 --yes
```

## Contexts

### context

Manage server contexts.

```bash
prisn context <command> [args]
```

**Subcommands:**
```
  list                       List contexts
  current                    Show current context
  use <name>                 Switch context
  add <name> [flags]         Add context
  delete <name>              Remove context
```

**Flags for add:**
```
      --server string        Server address
      --token string         API token
      --namespace string     Default namespace
```

**Examples:**
```bash
prisn context list
prisn context add prod --server prod.example.com:7331 --token prisn_xxx
prisn context use prod
prisn context current
```

## Server

### server

Run the prisn server.

```bash
prisn server [flags]
```

**Flags:**
```
      --addr string          Listen address (default: :7331)
      --data-dir string      Data directory
      --api-key string       Require API key for access
```

### cluster

Manage cluster membership.

```bash
prisn cluster <command>
```

**Subcommands:**
```
  status                     Show cluster status
  nodes                      List nodes
  join <addr>                Join cluster
  leave                      Leave cluster
```

## Utility

### check

Validate configuration files and scripts.

```bash
prisn check [file] [flags]
```

**Flags:**
```
  -f, --file string        Config file to validate (default: prisn.toml)
      --strict             Fail on warnings
```

**Examples:**
```bash
prisn check                      # Validate prisn.toml
prisn check -f production.toml   # Validate specific file
prisn check script.py            # Check script dependencies
prisn check --strict             # Treat warnings as errors
```

### init

Initialize a new prisn project by creating a prisn.toml from existing files.

```bash
prisn init [flags]
```

**Flags:**
```
      --force              Overwrite existing prisn.toml
      --runtime string     Default runtime (auto-detected if not set)
```

**Examples:**
```bash
prisn init                 # Create prisn.toml from detected scripts
prisn init --force         # Overwrite existing config
prisn init --runtime node  # Force Node.js as default runtime
```

### trigger

Manually trigger a job or cron job execution.

```bash
prisn trigger <name> [flags]
```

**Flags:**
```
      --wait               Wait for completion
      --timeout duration   Timeout when waiting (default: 5m)
  -e, --env strings        Override environment variables
```

**Examples:**
```bash
prisn trigger backup             # Trigger the backup job
prisn trigger backup --wait      # Trigger and wait for completion
prisn trigger backup -e DRY_RUN=true  # Trigger with env override
```

### runtime

Manage runtime configurations.

```bash
prisn runtime <command> [args]
```

**Subcommands:**
```
  list                     List available runtimes
  info <runtime>           Show runtime details
  detect <file>            Detect runtime for a file
  check <runtime>          Check if runtime is available
  pull <runtime>           Pull runtime from registry
  push <runtime>           Push runtime to registry
  remote                   List remote runtimes
  remote-delete <runtime>  Delete remote runtime
```

**Examples:**
```bash
prisn runtime list                    # Show all available runtimes
prisn runtime info python3            # Show Python3 runtime details
prisn runtime detect script.py        # Detect runtime for script
prisn runtime check node              # Check if Node.js is available
prisn runtime pull custom-python      # Pull custom runtime from registry
```

### doctor

Diagnose environment.

```bash
prisn doctor
```

### self-test

Run synthetic health checks.

```bash
prisn self-test [flags]
```

### version

Show version.

```bash
prisn version
```

### completion

Generate shell completions.

```bash
prisn completion <shell>
```

**Shells:** `bash`, `zsh`, `fish`, `powershell`

```bash
# Add to ~/.bashrc or ~/.zshrc
source <(prisn completion bash)
source <(prisn completion zsh)
```
