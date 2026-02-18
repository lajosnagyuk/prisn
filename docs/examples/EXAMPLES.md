# prisn Examples

**Real-world uses for a "just run my shit" platform**

## Quick Start

### Your First Deployment

```bash
# Create a simple Python web server
cat > hello.py << 'EOF'
from http.server import HTTPServer, BaseHTTPRequestHandler

class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        self.send_response(200)
        self.send_header('Content-Type', 'text/plain')
        self.end_headers()
        self.wfile.write(b'Hello from prisn!\n')

    def log_message(self, format, *args):
        print(f"{self.address_string()} - {args[0]}")

HTTPServer(('', 8080), Handler).serve_forever()
EOF

# Deploy it
prisn deploy hello.py --port 8080

# Check it's running
prisn get deploy
# NAME    REPLICAS   STATUS    PORT
# hello   1/1        Running   8080

# Test it
curl http://hello.default.prisn.local:8080
# Hello from prisn!

# Scale up
prisn scale hello 3

# Clean up
prisn delete deploy hello
```

## One-Off Scripts

### Run a Quick Script

```bash
# Run inline Python
prisn run -e "print(sum(range(100)))" --runtime python

# Run a local file
prisn run backup.sh

# Run with arguments
prisn run process.py -- --input data.csv --output results.json

# Run with environment variables
prisn run script.py -e API_KEY=abc123 -e DEBUG=true

# Run with a timeout
prisn run slow-task.py --timeout 5m

# Run and detach (don't wait for output)
prisn run long-job.sh --detach
# Job ID: job-abc123
# Use 'prisn logs job/job-abc123' to view output
```

### Database Backup Script

```bash
cat > backup.sh << 'EOF'
#!/bin/bash
set -e

DATE=$(date +%Y%m%d_%H%M%S)
BACKUP_FILE="/tmp/backup_${DATE}.sql"

echo "Starting backup at $(date)"
pg_dump "$DATABASE_URL" > "$BACKUP_FILE"
echo "Backup created: $BACKUP_FILE ($(wc -c < "$BACKUP_FILE") bytes)"

# Upload to S3 (if configured)
if [ -n "$S3_BUCKET" ]; then
    aws s3 cp "$BACKUP_FILE" "s3://${S3_BUCKET}/backups/"
    echo "Uploaded to S3"
fi

echo "Backup complete at $(date)"
EOF

# Run it
prisn run backup.sh \
  -e DATABASE_URL="postgres://user:pass@db:5432/mydb" \
  -e S3_BUCKET="my-backups"
```

## Scheduled Tasks

### Cron Jobs

```bash
# Deploy as a scheduled job (standard cron syntax)
prisn deploy cleanup.sh --schedule "0 3 * * *" --name nightly-cleanup
prisn deploy report.py --schedule "0 9 * * *" --name daily-report
prisn deploy metrics.py --schedule "0 * * * *" --name hourly-metrics
prisn deploy heartbeat.sh --schedule "*/5 * * * *" --name heartbeat

# View cron jobs
prisn get cron
# NAME             SCHEDULE      LAST RUN    NEXT RUN
# nightly-cleanup  0 3 * * *     2h ago      in 22h
# daily-report     0 9 * * *     14h ago     in 10h
# hourly-metrics   0 * * * *     45m ago     in 15m
# heartbeat        */5 * * * *   3m ago      in 2m

# View history
prisn get jobs --cron daily-report
# ID                    STATUS      STARTED     DURATION
# daily-report-abc123   Completed   yesterday   2m34s
# daily-report-def456   Completed   2 days ago  2m41s

# Pause a cron job
prisn scale nightly-cleanup 0

# Resume
prisn scale nightly-cleanup 1
```

### Log Rotation

```yaml
# log-rotate.yaml
apiVersion: prisn.io/v1
kind: CronJob
metadata:
  name: log-rotate
spec:
  schedule: "0 0 * * *"  # Daily at midnight
  jobTemplate:
    spec:
      runtime: bash
      source:
        inline: |
          #!/bin/bash
          find /prisn/shared/logs -name "*.log" -mtime +7 -delete
          echo "Cleaned logs older than 7 days"
      timeout: 10m
```

## Web Services

### Python Flask API

```python
# api.py
from flask import Flask, jsonify, request
import os

app = Flask(__name__)

items = []

@app.route('/health')
def health():
    return jsonify(status='healthy')

@app.route('/items', methods=['GET'])
def get_items():
    return jsonify(items=items)

@app.route('/items', methods=['POST'])
def add_item():
    item = request.json
    items.append(item)
    return jsonify(item=item), 201

if __name__ == '__main__':
    port = int(os.environ.get('PORT', 8080))
    app.run(host='0.0.0.0', port=port)
```

```bash
# Deploy with auto-detected port
prisn deploy api.py --port 8080 --replicas 3

# Or with manifest
cat > api-deployment.yaml << 'EOF'
apiVersion: prisn.io/v1
kind: Deployment
metadata:
  name: api
spec:
  runtime: python3
  source:
    file: ./api.py
  replicas: 3
  port: 8080
  healthCheck:
    http:
      path: /health
      port: 8080
    interval: 10s
  resources:
    requests:
      cpu: 100m
      memory: 128Mi
    limits:
      cpu: 500m
      memory: 256Mi
EOF

prisn apply -f api-deployment.yaml
```

### Node.js Express API

```javascript
// server.js
const express = require('express');
const app = express();
app.use(express.json());

const items = [];

app.get('/health', (req, res) => {
  res.json({ status: 'healthy' });
});

app.get('/items', (req, res) => {
  res.json({ items });
});

app.post('/items', (req, res) => {
  items.push(req.body);
  res.status(201).json({ item: req.body });
});

const port = process.env.PORT || 8080;
app.listen(port, () => {
  console.log(`Server running on port ${port}`);
});
```

```bash
prisn deploy server.js --port 8080 --replicas 2
```

## Workers and Background Jobs

### Task Queue Worker

```python
# worker.py
import os
import time
import json
import redis

REDIS_URL = os.environ.get('REDIS_URL', 'redis://localhost:6379')
QUEUE_NAME = os.environ.get('QUEUE_NAME', 'tasks')

r = redis.from_url(REDIS_URL)

print(f"Worker starting, listening on queue: {QUEUE_NAME}")

while True:
    # Blocking pop from queue
    _, task_json = r.blpop(QUEUE_NAME)
    task = json.loads(task_json)

    print(f"Processing task: {task['id']}")
    try:
        # Simulate work
        time.sleep(task.get('duration', 1))
        print(f"Task {task['id']} completed")
    except Exception as e:
        print(f"Task {task['id']} failed: {e}")
```

```bash
prisn deploy worker.py \
  --replicas 5 \
  -e REDIS_URL=redis://redis.default.prisn.local:6379 \
  -e QUEUE_NAME=high-priority

# Scale based on queue depth
prisn scale worker enough  # Auto-scale based on load
```

### Event Processor

```yaml
# event-processor.yaml
apiVersion: prisn.io/v1
kind: Deployment
metadata:
  name: event-processor
spec:
  runtime: node
  source:
    inline: |
      const Kafka = require('kafkajs').Kafka;

      const kafka = new Kafka({
        brokers: [process.env.KAFKA_BROKER]
      });

      const consumer = kafka.consumer({ groupId: 'event-processors' });

      async function run() {
        await consumer.connect();
        await consumer.subscribe({ topic: 'events' });

        await consumer.run({
          eachMessage: async ({ topic, partition, message }) => {
            console.log({
              topic,
              partition,
              key: message.key?.toString(),
              value: message.value.toString(),
            });
            // Process event...
          },
        });
      }

      run().catch(console.error);

  replicas:
    min: 2
    max: 20
    target:
      cpu: 60

  env:
    - name: KAFKA_BROKER
      value: kafka.default.prisn.local:9092

  resources:
    requests:
      cpu: 100m
      memory: 256Mi
    limits:
      cpu: 1
      memory: 512Mi
```

## IoT and Hardware

### Sensor Data Collector

```python
# sensor.py
import os
import time
import json
import requests

SENSOR_ID = os.environ.get('SENSOR_ID', 'sensor-1')
INTERVAL = int(os.environ.get('INTERVAL', 60))
API_URL = os.environ.get('API_URL')

def read_temperature():
    # Simulate reading from hardware
    # In reality: GPIO, I2C, serial port, etc.
    import random
    return 20 + random.random() * 10

def read_humidity():
    import random
    return 40 + random.random() * 30

print(f"Sensor {SENSOR_ID} starting, interval: {INTERVAL}s")

while True:
    data = {
        'sensor_id': SENSOR_ID,
        'timestamp': time.time(),
        'temperature': read_temperature(),
        'humidity': read_humidity(),
    }

    print(f"Reading: temp={data['temperature']:.1f}C, humidity={data['humidity']:.1f}%")

    if API_URL:
        try:
            requests.post(f"{API_URL}/readings", json=data)
        except Exception as e:
            print(f"Failed to send: {e}")

    time.sleep(INTERVAL)
```

```bash
# Deploy on a Raspberry Pi node
prisn deploy sensor.py \
  --name sensor-garage \
  -e SENSOR_ID=garage-1 \
  -e INTERVAL=30 \
  -e API_URL=http://api.default.prisn.local:8080 \
  --node-selector arch=arm64
```

### Coffee Machine Laser Pointer (Why Not?)

```python
# coffee-laser.py
import os
import time
import schedule

# Assume you have a servo/laser connected via GPIO
# This is clearly a joke but also... totally doable

def aim_laser():
    print("Aiming laser at coffee machine...")
    # move_servo(angle=45)
    # fire_laser(duration=0.5)
    print("Coffee machine activated!")

def turn_off_laser():
    print("Turning off laser")
    # move_servo(angle=0)

# Every day at 7am, wake up sequence
schedule.every().day.at("07:00").do(aim_laser)
schedule.every().day.at("07:01").do(turn_off_laser)

print("Coffee laser scheduler started")
while True:
    schedule.run_pending()
    time.sleep(1)
```

```bash
prisn deploy coffee-laser.py --node-selector has-laser=true
```

## Data Processing

### CSV Processor

```python
# process-csv.py
import sys
import csv
import json

input_file = sys.argv[1] if len(sys.argv) > 1 else '/prisn/data/input.csv'
output_file = sys.argv[2] if len(sys.argv) > 2 else '/prisn/data/output.json'

print(f"Processing {input_file}")

results = []
with open(input_file, 'r') as f:
    reader = csv.DictReader(f)
    for row in reader:
        # Transform
        row['processed'] = True
        row['total'] = float(row.get('price', 0)) * int(row.get('quantity', 1))
        results.append(row)

with open(output_file, 'w') as f:
    json.dump(results, f, indent=2)

print(f"Wrote {len(results)} records to {output_file}")
```

```bash
# Run as one-off job
prisn run process-csv.py \
  --volume data:/prisn/data \
  -- /prisn/data/sales.csv /prisn/data/processed.json
```

### Parallel Data Pipeline

```bash
# Split large file and process in parallel
cat > pipeline.sh << 'EOF'
#!/bin/bash
INPUT=$1
OUTPUT_DIR=$2

# Split into chunks
split -l 10000 "$INPUT" "${OUTPUT_DIR}/chunk_"

# Process each chunk (in reality, spawn prisn jobs)
for chunk in ${OUTPUT_DIR}/chunk_*; do
    echo "Processing $chunk"
    # prisn run process.py -- "$chunk" "${chunk}.processed"
done

# Combine results
cat ${OUTPUT_DIR}/chunk_*.processed > "${OUTPUT_DIR}/final.json"
EOF

prisn run pipeline.sh -- /data/huge-file.csv /data/output
```

## Custom Runners

### Lua Runner

```yaml
# lua-runner.yaml
apiVersion: prisn.io/v1
kind: Runner
metadata:
  name: lua
  namespace: prisn-system  # Cluster-wide
spec:
  detect:
    extensions: [".lua"]
  command: ["lua"]
  args: ["{script}"]
  requirements:
    commands: ["lua"]
```

```bash
prisn apply -f lua-runner.yaml

# Now you can run Lua scripts
cat > hello.lua << 'EOF'
print("Hello from Lua!")
for i = 1, 10 do
    print("Count: " .. i)
end
EOF

prisn run hello.lua
```

### R Runner (Data Science)

```yaml
apiVersion: prisn.io/v1
kind: Runner
metadata:
  name: r
spec:
  detect:
    extensions: [".r", ".R"]
  command: ["Rscript"]
  args: ["{script}"]
  requirements:
    commands: ["Rscript"]
  resources:
    limits:
      cpu: 4
      memory: 8Gi
```

### Julia Runner (Scientific Computing)

```yaml
apiVersion: prisn.io/v1
kind: Runner
metadata:
  name: julia
spec:
  detect:
    extensions: [".jl"]
  command: ["julia"]
  args: ["{script}"]
  requirements:
    commands: ["julia"]
```

## Multi-Service Application

### Complete Stack

```yaml
# stack.yaml
---
apiVersion: prisn.io/v1
kind: Secret
metadata:
  name: app-secrets
spec:
  stringData:
    database-url: postgres://user:pass@postgres:5432/app
    redis-url: redis://redis:6379
    jwt-secret: super-secret-key

---
apiVersion: prisn.io/v1
kind: Deployment
metadata:
  name: api
spec:
  runtime: python3
  source:
    file: ./api/main.py
  replicas: 3
  port: 8080
  env:
    - name: DATABASE_URL
      secretRef:
        name: app-secrets
        key: database-url
    - name: REDIS_URL
      secretRef:
        name: app-secrets
        key: redis-url
  healthCheck:
    http:
      path: /health
      port: 8080
  expose:
    type: ingress
    host: api.example.com

---
apiVersion: prisn.io/v1
kind: Deployment
metadata:
  name: worker
spec:
  runtime: python3
  source:
    file: ./worker/main.py
  replicas:
    min: 2
    max: 10
    target:
      cpu: 70
  env:
    - name: DATABASE_URL
      secretRef:
        name: app-secrets
        key: database-url
    - name: REDIS_URL
      secretRef:
        name: app-secrets
        key: redis-url

---
apiVersion: prisn.io/v1
kind: CronJob
metadata:
  name: daily-report
spec:
  schedule: "0 9 * * *"
  jobTemplate:
    spec:
      runtime: python3
      source:
        file: ./jobs/daily-report.py
      env:
        - name: DATABASE_URL
          secretRef:
            name: app-secrets
            key: database-url
```

```bash
# Deploy entire stack
prisn apply -f stack.yaml

# View all resources
prisn get all
```

## Heterogeneous Cluster

### Targeting Different Nodes

```bash
# Deploy to Linux x86_64 nodes only
prisn deploy compute.py --node-selector os=linux,arch=amd64

# Deploy to nodes with GPUs
prisn deploy ml-inference.py --node-selector gpu=nvidia

# Deploy to ARM nodes (Raspberry Pi, etc.)
prisn deploy edge-agent.py --node-selector arch=arm64

# Deploy to FreeBSD nodes
prisn deploy network-monitor.sh --node-selector os=freebsd
```

### Mixed Architecture Deployment

```yaml
apiVersion: prisn.io/v1
kind: Deployment
metadata:
  name: universal-worker
spec:
  runtime: python3
  source:
    inline: |
      import platform
      import os
      print(f"Running on {platform.system()} {platform.machine()}")
      print(f"Node: {os.environ.get('PRISN_NODE_NAME', 'unknown')}")
      # ... do work

  replicas: 10

  # Let prisn spread across all available nodes
  placement:
    topologySpreadConstraints:
      - maxSkew: 2
        topologyKey: arch
        whenUnsatisfiable: ScheduleAnyway
```

## Monitoring and Debugging

### View Logs

```bash
# Follow logs
prisn logs api -f

# Last 100 lines
prisn logs api --tail 100

# Logs from last hour
prisn logs api --since 1h

# Logs from specific instance
prisn logs api-abc123

# All logs in namespace
prisn logs --all

# With timestamps
prisn logs api -t
```

### Exec into Running Instance

```bash
# Get a shell
prisn exec api-abc123 -- /bin/sh

# Run a command
prisn exec api-abc123 -- python -c "import app; print(app.status())"

# Run in any instance of deployment
prisn exec api -- python manage.py shell
```

### Check Metrics

```bash
# View deployment metrics
prisn top deploy api
# NAME   CPU    MEMORY   REQUESTS/s   ERRORS
# api    450m   384Mi    1234         0.1%

# View node metrics
prisn top nodes
# NAME     CPU     MEMORY    PODS    STATUS
# node-1   2.1/4   6.2/8Gi   12      Ready
# node-2   1.8/4   5.1/8Gi   10      Ready
```

### Debug Deployment

```bash
# Describe for detailed info
prisn describe deploy api

# Check events
prisn events --resource deploy/api

# Check why instances aren't running
prisn get deploy api -o wide

# Check scheduler decisions
prisn describe deploy api | grep -A10 "Scheduling"
```
