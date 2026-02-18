#!/usr/bin/env python3
"""
Dashboard - A live system monitoring dashboard.

Deploy: prisn deploy examples/dashboard.py --name dashboard --port 8080
Visit:  http://localhost:8080
"""
from http.server import HTTPServer, BaseHTTPRequestHandler
import json
import os
import socket
import platform
from datetime import datetime
import sys

# Track request metrics
metrics = {
    "requests": 0,
    "started": datetime.now().isoformat(),
    "endpoints": {},
}


def get_system_info():
    """Gather system information."""
    return {
        "hostname": socket.gethostname(),
        "platform": platform.platform(),
        "python": sys.version.split()[0],
        "architecture": platform.machine(),
        "processor": platform.processor() or "unknown",
    }


def get_environment():
    """Get relevant environment variables."""
    relevant = ["PORT", "HOSTNAME", "HOME", "USER", "PATH", "PRISN_CONTEXT"]
    return {k: os.environ.get(k, "not set") for k in relevant}


class DashboardHandler(BaseHTTPRequestHandler):
    def log_message(self, format, *args):
        pass

    def do_GET(self):
        metrics["requests"] += 1
        metrics["endpoints"][self.path] = metrics["endpoints"].get(self.path, 0) + 1

        if self.path == "/api/system":
            self.send_json(get_system_info())
            return

        if self.path == "/api/env":
            self.send_json(get_environment())
            return

        if self.path == "/api/metrics":
            self.send_json({
                "total_requests": metrics["requests"],
                "uptime_since": metrics["started"],
                "endpoints": metrics["endpoints"],
            })
            return

        if self.path == "/health":
            self.send_json({"status": "healthy", "timestamp": datetime.now().isoformat()})
            return

        # Main dashboard
        sys_info = get_system_info()
        env_info = get_environment()

        html = f"""
<!DOCTYPE html>
<html>
<head>
    <title>System Dashboard</title>
    <meta http-equiv="refresh" content="10">
    <style>
        * {{ box-sizing: border-box; margin: 0; padding: 0; }}
        body {{
            font-family: 'SF Mono', 'Fira Code', monospace;
            background: #0d1117;
            color: #c9d1d9;
            padding: 20px;
            min-height: 100vh;
        }}
        .header {{
            text-align: center;
            padding: 20px;
            border-bottom: 1px solid #30363d;
            margin-bottom: 20px;
        }}
        .header h1 {{
            color: #58a6ff;
            font-size: 28px;
        }}
        .header .time {{
            color: #8b949e;
            margin-top: 5px;
        }}
        .grid {{
            display: grid;
            grid-template-columns: repeat(auto-fit, minmax(350px, 1fr));
            gap: 20px;
            max-width: 1200px;
            margin: 0 auto;
        }}
        .card {{
            background: #161b22;
            border: 1px solid #30363d;
            border-radius: 8px;
            padding: 20px;
        }}
        .card h2 {{
            color: #58a6ff;
            font-size: 14px;
            text-transform: uppercase;
            letter-spacing: 1px;
            margin-bottom: 15px;
            padding-bottom: 10px;
            border-bottom: 1px solid #30363d;
        }}
        .stat {{
            display: flex;
            justify-content: space-between;
            padding: 8px 0;
            border-bottom: 1px solid #21262d;
        }}
        .stat:last-child {{ border-bottom: none; }}
        .stat-label {{ color: #8b949e; }}
        .stat-value {{
            color: #7ee787;
            font-weight: bold;
        }}
        .stat-value.highlight {{ color: #ffa657; }}
        .metric-big {{
            text-align: center;
            padding: 20px;
        }}
        .metric-big .value {{
            font-size: 48px;
            color: #7ee787;
            font-weight: bold;
        }}
        .metric-big .label {{
            color: #8b949e;
            margin-top: 5px;
        }}
        .endpoints {{
            max-height: 200px;
            overflow-y: auto;
        }}
        .bar {{
            background: #21262d;
            border-radius: 4px;
            height: 8px;
            margin-top: 4px;
        }}
        .bar-fill {{
            background: linear-gradient(90deg, #238636, #7ee787);
            height: 100%;
            border-radius: 4px;
            transition: width 0.3s;
        }}
        .footer {{
            text-align: center;
            padding: 20px;
            color: #8b949e;
            margin-top: 20px;
        }}
        .pulse {{
            animation: pulse 2s infinite;
        }}
        @keyframes pulse {{
            0%, 100% {{ opacity: 1; }}
            50% {{ opacity: 0.5; }}
        }}
    </style>
</head>
<body>
    <div class="header">
        <h1>System Dashboard</h1>
        <div class="time">{datetime.now().strftime("%Y-%m-%d %H:%M:%S")} <span class="pulse">LIVE</span></div>
    </div>

    <div class="grid">
        <div class="card">
            <h2>System Information</h2>
            <div class="stat">
                <span class="stat-label">Hostname</span>
                <span class="stat-value">{sys_info['hostname']}</span>
            </div>
            <div class="stat">
                <span class="stat-label">Platform</span>
                <span class="stat-value">{sys_info['platform'][:30]}</span>
            </div>
            <div class="stat">
                <span class="stat-label">Architecture</span>
                <span class="stat-value">{sys_info['architecture']}</span>
            </div>
            <div class="stat">
                <span class="stat-label">Python</span>
                <span class="stat-value">{sys_info['python']}</span>
            </div>
        </div>

        <div class="card">
            <h2>Request Metrics</h2>
            <div class="metric-big">
                <div class="value">{metrics['requests']}</div>
                <div class="label">Total Requests</div>
            </div>
            <div class="stat">
                <span class="stat-label">Uptime Since</span>
                <span class="stat-value highlight">{metrics['started'][:19]}</span>
            </div>
        </div>

        <div class="card">
            <h2>Environment</h2>
            {''.join(f'''
            <div class="stat">
                <span class="stat-label">{k}</span>
                <span class="stat-value">{(v[:25] + "...") if len(str(v)) > 25 else v}</span>
            </div>
            ''' for k, v in env_info.items() if v != "not set")}
        </div>

        <div class="card">
            <h2>Top Endpoints</h2>
            <div class="endpoints">
                {''.join(f'''
                <div class="stat">
                    <span class="stat-label">{path}</span>
                    <span class="stat-value">{count}</span>
                </div>
                <div class="bar">
                    <div class="bar-fill" style="width: {min(100, count * 10)}%"></div>
                </div>
                ''' for path, count in sorted(metrics['endpoints'].items(), key=lambda x: -x[1])[:5])}
            </div>
        </div>
    </div>

    <div class="footer">
        Powered by <strong>prisn</strong> |
        <a href="/api/system" style="color:#58a6ff">/api/system</a> |
        <a href="/api/metrics" style="color:#58a6ff">/api/metrics</a> |
        <a href="/health" style="color:#58a6ff">/health</a>
    </div>
</body>
</html>
"""
        self.send_response(200)
        self.send_header("Content-Type", "text/html")
        self.end_headers()
        self.wfile.write(html.encode())

    def send_json(self, data):
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        self.wfile.write(json.dumps(data, indent=2).encode())


if __name__ == "__main__":
    port = int(os.environ.get("PORT", 8080))
    server = HTTPServer(("0.0.0.0", port), DashboardHandler)
    print(f"Dashboard running on port {port}")
    print(f"Open http://localhost:{port}")
    server.serve_forever()
