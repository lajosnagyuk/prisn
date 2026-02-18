# Quickstart

**Get running in 5 minutes.**

## Install

```bash
# From source
go install github.com/lajosnagyuk/prisn/cmd/prisn@latest
```

Or download from [releases](https://github.com/lajosnagyuk/prisn/releases).

## 1. Run a Script

Create a Python script:

```python
# hello.py
import os
print(f"Hello from {os.environ.get('USER', 'prisn')}!")
```

Run it:

```bash
prisn run hello.py
# Hello from yourname!
```

That's it. prisn detected Python and ran it.

## 2. Add Dependencies

Create a script with dependencies:

```python
# weather.py
import requests

resp = requests.get("https://wttr.in/London?format=3")
print(resp.text)
```

Add requirements:

```
# requirements.txt
requests
```

Run it:

```bash
prisn run weather.py
# London: +12°C
```

prisn automatically:
1. Detected `requirements.txt`
2. Created a virtual environment
3. Installed `requests`
4. Ran your script

## 3. Deploy a Service

Create a simple web server:

```python
# api.py
from http.server import HTTPServer, BaseHTTPRequestHandler
import os

class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        self.send_response(200)
        self.end_headers()
        self.wfile.write(b"Hello from prisn!\n")

port = int(os.environ.get("PORT", 8080))
print(f"Listening on :{port}")
HTTPServer(("", port), Handler).serve_forever()
```

Deploy it:

```bash
prisn deploy api.py --port 8080
```

Check status:

```bash
prisn status api
# NAME   REPLICAS   STATUS    PORT
# api    1/1        Running   8080
```

Test it:

```bash
curl http://localhost:8080
# Hello from prisn!
```

## 4. Scale It

```bash
prisn scale api 3
prisn status api
# NAME   REPLICAS   STATUS    PORT
# api    3/3        Running   8080
```

## 5. Add a Scheduled Job

```bash
# Run every hour
prisn deploy backup.sh --name backup --schedule "0 * * * *"

# Check scheduled jobs
prisn get cronjob
```

## 6. Use a Config File

For anything more than a single script, use `prisn.toml`:

```toml
# prisn.toml

[service.api]
source = "api.py"
port = 8080
replicas = 2

[service.api.env]
LOG_LEVEL = "info"

[job.backup]
source = "backup.sh"
schedule = "0 3 * * *"
```

Deploy everything:

```bash
prisn apply -f prisn.toml
```

## 7. View Logs

```bash
# Follow logs
prisn logs api -f

# Last 50 lines
prisn logs api --tail 50
```

## 8. Clean Up

```bash
# Stop a service
prisn scale api 0

# Delete it
prisn delete service api

# Delete everything
prisn delete -f prisn.toml
```

## Next Steps

- **[Configuration](CONFIGURATION.md)** - Full prisn.toml reference
- **[CLI Reference](CLI-REFERENCE.md)** - All commands
- **[Secrets](SECRETS.md)** - Managing sensitive data
- **[Examples](examples/EXAMPLES.md)** - Real-world patterns
