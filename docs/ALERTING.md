# Alerting Guide for SREs

This document provides recommended alerts and thresholds for monitoring prisn in production.

## Metrics Endpoint

prisn exposes Prometheus-format metrics at `/metrics` on the server port (default: 7331).

```bash
curl http://localhost:7331/metrics
```

## Recommended Alerts

### Critical Alerts (Page immediately)

#### 1. Server Down
```yaml
alert: PrisnServerDown
expr: up{job="prisn"} == 0
for: 30s
labels:
  severity: critical
annotations:
  summary: "prisn server is down"
  runbook: "Check process: systemctl status prisn; Check logs: journalctl -u prisn -n 100"
```

#### 2. High Error Rate
```yaml
alert: PrisnHighErrorRate
expr: |
  (
    sum(rate(prisn_execution_errors_total[5m]))
    /
    sum(rate(prisn_execution_total[5m]))
  ) > 0.1
for: 5m
labels:
  severity: critical
annotations:
  summary: "prisn error rate above 10%"
  runbook: "Check recent errors: prisn errors --since 1h"
```

#### 3. Database Unreachable
```yaml
alert: PrisnDatabaseError
expr: prisn_database_errors_total > 0
for: 1m
labels:
  severity: critical
annotations:
  summary: "prisn cannot access SQLite database"
  runbook: "Check disk space: df -h ~/.prisn; Check permissions: ls -la ~/.prisn/state.db"
```

### Warning Alerts (Investigate during business hours)

#### 4. Deployment Crash Looping
```yaml
alert: PrisnDeploymentCrashLoop
expr: |
  increase(prisn_service_restarts_total[5m]) > 3
for: 5m
labels:
  severity: warning
annotations:
  summary: "Deployment {{ $labels.deployment }} restarting frequently"
  runbook: "Check logs: prisn logs {{ $labels.deployment }}; Check history: prisn history {{ $labels.deployment }}"
```

#### 5. Execution Duration Degradation
```yaml
alert: PrisnSlowExecutions
expr: |
  histogram_quantile(0.99,
    rate(prisn_execution_duration_seconds_bucket[5m])
  ) > 30
for: 10m
labels:
  severity: warning
annotations:
  summary: "prisn execution p99 latency above 30s"
  runbook: "Check resource usage: prisn status; Consider scaling"
```

#### 6. High Memory Usage
```yaml
alert: PrisnHighMemory
expr: prisn_process_resident_memory_bytes > 500 * 1024 * 1024
for: 5m
labels:
  severity: warning
annotations:
  summary: "prisn using more than 500MB memory"
  runbook: "Check deployment count: prisn get deploy; Consider cleanup"
```

#### 7. Scheduler Lag
```yaml
alert: PrisnSchedulerLag
expr: prisn_scheduler_lag_seconds > 60
for: 5m
labels:
  severity: warning
annotations:
  summary: "prisn scheduler running behind"
  runbook: "Check cron jobs: prisn get cronjobs; Check system load"
```

#### 8. Disk Space Low
```yaml
alert: PrisnDiskSpaceLow
expr: prisn_disk_usage_bytes / prisn_disk_total_bytes > 0.85
for: 10m
labels:
  severity: warning
annotations:
  summary: "prisn data directory above 85% capacity"
  runbook: "Clean old versions: prisn cleanup --keep 3; Check logs"
```

## Key Metrics Reference

| Metric | Type | Description |
|--------|------|-------------|
| `prisn_deployments_total` | Gauge | Total number of deployments |
| `prisn_services_running` | Gauge | Currently running services |
| `prisn_execution_total` | Counter | Total executions |
| `prisn_execution_errors_total` | Counter | Failed executions |
| `prisn_execution_duration_seconds` | Histogram | Execution duration |
| `prisn_service_restarts_total` | Counter | Service restart count |
| `prisn_scheduler_lag_seconds` | Gauge | Time since last scheduler tick |
| `prisn_api_requests_total` | Counter | API request count |
| `prisn_api_latency_seconds` | Histogram | API response time |

## Health Check Endpoints

### `/health`
Returns server health status. Use for load balancer health checks.

```bash
curl http://localhost:7331/health
# {"status":"healthy","node_id":"node-abc123","uptime":"2h34m"}
```

### `/ready`
Returns readiness status. Returns 503 during startup or shutdown.

```bash
curl http://localhost:7331/ready
# {"ready":true}
```

## Synthetic Monitoring

Use `prisn self-test` for synthetic monitoring:

```bash
# Run every 5 minutes from monitoring system
prisn self-test -o json
```

Example Prometheus blackbox config:
```yaml
modules:
  prisn_health:
    prober: http
    http:
      preferred_ip_protocol: "ip4"
      valid_status_codes: [200]
      fail_if_body_not_matches_regexp:
        - '"status":"healthy"'
```

## Runbook Quick Reference

### Server Won't Start
1. Check logs: `journalctl -u prisn -n 100`
2. Check port: `lsof -i :7331`
3. Check database: `sqlite3 ~/.prisn/state.db "SELECT 1"`
4. Try doctor: `prisn doctor`

### Deployment Keeps Crashing
1. Check logs: `prisn logs <deployment> --tail 100`
2. Check history: `prisn history <deployment>`
3. Check resources: Does it need more memory?
4. Try rollback: `prisn rollback <deployment>`

### High Latency
1. Check current load: `prisn status`
2. Check slow deployments: `prisn errors`
3. Check system resources: `top`, `iostat`
4. Scale down concurrent jobs if needed

### Database Corruption
1. Stop server: `systemctl stop prisn`
2. Backup: `cp ~/.prisn/state.db ~/.prisn/state.db.bak`
3. Try recovery: `sqlite3 ~/.prisn/state.db "PRAGMA integrity_check"`
4. If failed: `rm ~/.prisn/state.db` (will recreate, lose state)

## Dashboard Panels

Recommended Grafana panels:

1. **Service Status** - Table showing all deployments and their state
2. **Execution Rate** - Graph of executions/minute
3. **Error Rate** - Graph of errors/minute with threshold line
4. **P99 Latency** - Histogram of execution duration
5. **Memory Usage** - Time series of process memory
6. **Restart Rate** - Counter of service restarts
7. **Scheduler Health** - Lag metric over time

## SLA Recommendations

| Tier | Availability | Response Time | Error Rate |
|------|--------------|---------------|------------|
| Production | 99.9% | < 100ms p99 | < 0.1% |
| Staging | 99% | < 500ms p99 | < 1% |
| Development | Best effort | - | - |

---

*Last updated: 2026-01-27*
