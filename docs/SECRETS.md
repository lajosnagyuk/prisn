# Secrets & Environment Variables

**Managing sensitive data in prisn**

## Quick Start

```bash
# Set env vars for a deployment
prisn env set api DATABASE_URL=postgres://localhost/db LOG_LEVEL=info

# Or create a reusable secret
prisn secret create db-creds --from-literal url=postgres://user:pass@host/db

# Reference in prisn.toml
# DATABASE_URL = { secret = "db-creds", key = "url" }
```

## Environment Variables

### Inline with deploy

```bash
prisn deploy api.py --port 8080 -e LOG_LEVEL=info -e DEBUG=false
```

### After deployment

```bash
prisn env set api LOG_LEVEL=debug
prisn env get api
prisn env unset api DEBUG
```

### In prisn.toml

```toml
[service.api]
source = "api.py"

[service.api.env]
LOG_LEVEL = "info"
PORT = "8080"
DEBUG = "false"
```

## .env Files

Load environment from a file:

**.env**
```
DATABASE_URL=postgres://localhost/dev
REDIS_URL=redis://localhost:6379
API_KEY=dev-key-123
LOG_LEVEL=debug
```

**prisn.toml**
```toml
[service.api]
source = "api.py"
env_file = ".env"
```

Multiple files (later files override earlier):

```toml
[service.api]
source = "api.py"
env_file = [".env", ".env.local"]
```

## Secrets

For sensitive data that shouldn't be in config files.

### Create a secret

```bash
# From literal values
prisn secret create db-creds \
  --from-literal url=postgres://user:pass@host/db \
  --from-literal password=hunter2

# From a file
prisn secret create tls-cert \
  --from-file cert.pem=./certificate.pem \
  --from-file key.pem=./private-key.pem

# From a .env file
prisn secret create app-secrets --from-env-file .env.prod
```

### List secrets

```bash
prisn secret list
# NAME          KEYS              AGE
# db-creds      url, password     2d
# tls-cert      cert.pem, key.pem 1d
```

### Reference in config

```toml
[service.api]
source = "api.py"

[service.api.env]
# Plain env var
LOG_LEVEL = "info"

# From secret
DATABASE_URL = { secret = "db-creds", key = "url" }
DB_PASSWORD = { secret = "db-creds", key = "password" }
```

### Delete a secret

```bash
prisn secret delete db-creds
```

## Patterns

### Development vs Production

**Project structure:**
```
my-app/
  prisn.toml         # Base config
  prisn.prod.toml    # Production overrides
  .env               # Dev secrets (gitignored)
  .env.prod          # Prod secrets (gitignored)
```

**.gitignore**
```
.env
.env.*
```

**prisn.toml** (development):
```toml
[service.api]
source = "api.py"
port = 8080
replicas = 1
env_file = ".env"

[service.api.env]
LOG_LEVEL = "debug"
```

**prisn.prod.toml** (production):
```toml
[service.api]
source = "api.py"
port = 8080
replicas = 5
env_file = ".env.prod"

[service.api.env]
LOG_LEVEL = "warn"
DATABASE_URL = { secret = "prod-db", key = "url" }
```

```bash
# Development
prisn apply

# Production
prisn apply -f prisn.prod.toml
```

### Shared Secrets

Multiple services can reference the same secret:

```bash
prisn secret create shared-creds --from-literal api-key=xxx
```

```toml
[service.api]
source = "api.py"
[service.api.env]
API_KEY = { secret = "shared-creds", key = "api-key" }

[service.worker]
source = "worker.py"
[service.worker.env]
API_KEY = { secret = "shared-creds", key = "api-key" }
```

### Environment from Host

Pass through host environment variables:

```bash
export DATABASE_URL=postgres://localhost/dev
prisn deploy api.py --port 8080 -e DATABASE_URL
```

Or with variable substitution:

```toml
[service.api.env]
DATABASE_URL = "${DATABASE_URL}"
REPLICAS = "${REPLICAS:-2}"  # Default value
```

```bash
DATABASE_URL=postgres://prod/db REPLICAS=5 prisn apply
```

## Security Best Practices

1. **Never commit secrets** - Use `.gitignore` for `.env*` files
2. **Use secrets for sensitive data** - Not plain env vars in prisn.toml
3. **Rotate secrets regularly** - Update secrets, redeploy affected services
4. **Limit access** - Use RBAC to control who can read/write secrets
5. **Audit access** - Check `prisn events` for secret access

## Secret Rotation

```bash
# Update a secret
prisn secret create db-creds \
  --from-literal url=postgres://user:newpass@host/db \
  --from-literal password=newpassword

# Restart affected deployments to pick up changes
prisn scale api 0 && prisn scale api 3
```

## Viewing Secret Values

By default, prisn doesn't show secret values:

```bash
prisn secret get db-creds
# NAME       KEYS
# db-creds   url, password
```

To see actual values (requires admin role):

```bash
prisn secret get db-creds --show-values
# NAME       KEY        VALUE
# db-creds   url        postgres://user:pass@host/db
# db-creds   password   hunter2
```
