# Role-Based Access Control (RBAC)

**Managing access to prisn**

## Overview

prisn uses token-based authentication with role-based permissions. Tokens grant access to specific resources within specific namespaces.

## Roles

| Role | Description |
|------|-------------|
| **admin** | Full access to all resources and operations |
| **developer** | Manage deployments, jobs, and secrets in allowed namespaces |
| **viewer** | Read-only access to allowed namespaces |

### Permission Matrix

| Resource | admin | developer | viewer |
|----------|-------|-----------|--------|
| Deployments | CRUD + scale | CRUD + scale | read, list |
| Jobs | CRUD | CRUD | read, list |
| Cron Jobs | CRUD | CRUD | read, list |
| Executions | read, list, logs | read, list, logs | read, list, logs |
| Secrets | CRUD | CRUD | - |
| Tokens | CRUD | - | - |
| Namespaces | CRUD | - | - |

## Creating Tokens

### Basic token

```bash
prisn token create \
  --name "CI/CD Pipeline" \
  --role developer \
  --namespace default
```

Output:
```
Token created successfully

  ID:         a1b2c3d4e5f6...
  Name:       CI/CD Pipeline
  Role:       developer
  Namespaces: default

Save this secret - it will not be shown again:

  prisn_7f8a9b0c1d2e3f4a5b6c7d8e9f0a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8
```

### Token with multiple namespaces

```bash
prisn token create \
  --name "Dev Team" \
  --role developer \
  --namespace default \
  --namespace staging
```

### Admin token with all namespaces

```bash
prisn token create \
  --name "Admin" \
  --role admin \
  --namespace "*"
```

### Expiring token

```bash
prisn token create \
  --name "Temp Access" \
  --role viewer \
  --namespace default \
  --expires-in 24h
```

Expiration formats: `1h`, `24h`, `7d`, `30d`

## Using Tokens

### With CLI

```bash
# Add a context with the token
prisn context add prod \
  --server prod.example.com:7331 \
  --token prisn_7f8a9b0c1d2e...

# Use the context
prisn -c prod get deploy
```

### With API

```bash
curl -H "Authorization: Bearer prisn_7f8a9b0c1d2e..." \
  https://prod.example.com:7331/api/v1/deployments
```

### Environment variable

```bash
export PRISN_TOKEN=prisn_7f8a9b0c1d2e...
prisn get deploy
```

## Managing Tokens

### List all tokens

```bash
prisn token list
# ID                   NAME              ROLE        NAMESPACES     STATUS
# a1b2c3d4e5f6...      CI/CD Pipeline    developer   default        active
# b2c3d4e5f6a7...      Dev Team          developer   default,stag.  active
# c3d4e5f6a7b8...      Admin             admin       *              active
```

### Filter by role

```bash
prisn token list --role admin
prisn token list --role developer
```

### Get token details

```bash
prisn token get a1b2c3d4e5f6
# ID:         a1b2c3d4e5f6...
# Name:       CI/CD Pipeline
# Role:       developer
# Namespaces: default
# Created:    2024-01-15T10:30:00Z
# Expires:    never
# Last Used:  2024-01-16T08:45:00Z
# Status:     active
```

### Revoke a token

Revoked tokens can no longer authenticate but remain in the audit log.

```bash
prisn token revoke a1b2c3d4e5f6
# Token a1b2c3d4e5f6 revoked
```

### Delete a token

Permanently removes a token.

```bash
prisn token delete a1b2c3d4e5f6
# Are you sure? Type 'yes' to confirm: yes
# Token a1b2c3d4e5f6 deleted

# Skip confirmation
prisn token delete a1b2c3d4e5f6 --yes
```

## Namespaces

Namespaces provide isolation between teams or environments.

### Using namespaces

Namespaces are created implicitly when you deploy to them.
Use the `-n` flag to target a specific namespace:

### Deploy to namespace

```bash
prisn deploy api.py --port 8080 -n staging
prisn -n production deploy api.py --port 8080
```

### Token namespace access

```bash
# Access to specific namespaces
prisn token create --name "Staging Dev" --role developer \
  --namespace staging

# Access to all namespaces (admin only)
prisn token create --name "Platform Admin" --role admin \
  --namespace "*"
```

## Server Configuration

### Require authentication

```bash
# Start server with API key requirement
prisn server --api-key "bootstrap-key-xxx"
```

The bootstrap key acts as an admin token for initial setup.

### Create first admin token

```bash
# Using bootstrap key
prisn context add local --server localhost:7331 --token bootstrap-key-xxx

# Create proper admin token
prisn token create --name "Admin" --role admin --namespace "*"

# Save the token, then remove bootstrap key
# Restart server without --api-key
```

## Best Practices

1. **Use namespaces** - Separate environments (dev, staging, prod)
2. **Least privilege** - Give users the minimum role they need
3. **Scope tokens** - Limit tokens to specific namespaces
4. **Rotate tokens** - Regularly create new tokens and revoke old ones
5. **Audit access** - Review `prisn events` for security events
6. **Expire tokens** - Use `--expires-in` for temporary access

## Audit Events

View authentication and authorization events:

```bash
prisn events --action token
prisn events --resource token
```

## Troubleshooting

### "Permission denied" error

1. Check token role: `prisn token get <id>`
2. Check namespace access: Does the token include the target namespace?
3. Check action: Does the role allow this action on this resource?

### Token not working

1. Is it revoked? `prisn token get <id>` shows status
2. Is it expired? Check `Expires` field
3. Is it the right server? Check `prisn context current`

### Lost admin access

If you've lost all admin tokens:

1. Stop the server
2. Restart with `--api-key <new-bootstrap-key>`
3. Create new admin token
4. Restart without `--api-key`
