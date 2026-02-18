# prisn Examples

Quick examples to get you started with prisn.

## 1. Hello World (No Dependencies)

```bash
prisn run examples/hello.py
# Output: Hello, World!

prisn run examples/hello.py -e NAME=prisn
# Output: Hello, prisn!
```

## 2. Script with Dependencies

prisn automatically detects `requirements.txt` and installs dependencies:

```bash
prisn run examples/with-deps/script.py
# First run: Creates venv, installs requests (~3-5s)
# Subsequent runs: Uses cached venv (~200ms)
```

## 3. Web API Examples

More complex examples showing real-world patterns:

- **hit-counter.py** - Redis-based counter (requires Redis)
- **fortune-api.py** - Simple HTTP API
- **guestbook.py** - Web app with persistence
- **dashboard.py** - Metrics dashboard

Deploy any of these as a service:

```bash
prisn deploy examples/fortune-api.py --port 8080
prisn status
curl http://localhost:8080
```

## 4. Custom Runtimes

See `runtimes.toml` for examples of custom runtime configurations.
