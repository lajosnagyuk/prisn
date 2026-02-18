#!/usr/bin/env python3
"""
Hit Counter - A simple visitor counter web app.

Deploy: prisn deploy examples/hit-counter.py --name counter --port 8080
Visit:  curl localhost:8080
"""
from http.server import HTTPServer, BaseHTTPRequestHandler
import json
import os
from datetime import datetime

# Store hits in memory (resets on restart)
# In production, you'd use Redis or a database
state = {
    "total_hits": 0,
    "unique_visitors": set(),
    "recent_visits": [],
    "started_at": datetime.now().isoformat(),
}


class CounterHandler(BaseHTTPRequestHandler):
    def log_message(self, format, *args):
        pass  # Silence default logging

    def do_GET(self):
        visitor_ip = self.client_address[0]
        state["total_hits"] += 1
        state["unique_visitors"].add(visitor_ip)
        state["recent_visits"].append({
            "time": datetime.now().isoformat(),
            "ip": visitor_ip,
            "path": self.path,
        })
        # Keep only last 10 visits
        state["recent_visits"] = state["recent_visits"][-10:]

        if self.path == "/stats":
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.end_headers()
            stats = {
                "total_hits": state["total_hits"],
                "unique_visitors": len(state["unique_visitors"]),
                "uptime_since": state["started_at"],
                "recent_visits": state["recent_visits"][-5:],
            }
            self.wfile.write(json.dumps(stats, indent=2).encode())
            return

        # Fun ASCII counter display
        count_str = str(state["total_hits"])
        digits = {
            "0": ["  ###  ", " #   # ", "#     #", "#     #", "#     #", " #   # ", "  ###  "],
            "1": ["   #   ", "  ##   ", " # #   ", "   #   ", "   #   ", "   #   ", " ##### "],
            "2": [" ##### ", "#     #", "      #", " ##### ", "#      ", "#      ", "#######"],
            "3": [" ##### ", "#     #", "      #", " ##### ", "      #", "#     #", " ##### "],
            "4": ["#      ", "#    # ", "#    # ", "#######", "     # ", "     # ", "     # "],
            "5": ["#######", "#      ", "#      ", " ##### ", "      #", "#     #", " ##### "],
            "6": [" ##### ", "#     #", "#      ", "###### ", "#     #", "#     #", " ##### "],
            "7": ["#######", "#    # ", "    #  ", "   #   ", "  #    ", "  #    ", "  #    "],
            "8": [" ##### ", "#     #", "#     #", " ##### ", "#     #", "#     #", " ##### "],
            "9": [" ##### ", "#     #", "#     #", " ######", "      #", "#     #", " ##### "],
        }

        ascii_lines = [""] * 7
        for digit in count_str:
            for i, line in enumerate(digits.get(digit, ["       "] * 7)):
                ascii_lines[i] += line + "  "

        response = f"""
<html>
<head>
    <title>Visitor Counter</title>
    <meta http-equiv="refresh" content="5">
    <style>
        body {{
            background: #1a1a2e;
            color: #0f0;
            font-family: monospace;
            display: flex;
            flex-direction: column;
            align-items: center;
            justify-content: center;
            min-height: 100vh;
            margin: 0;
        }}
        pre {{ font-size: 14px; line-height: 1.2; }}
        .label {{ color: #fff; font-size: 24px; margin: 20px; }}
        .stats {{ color: #888; font-size: 14px; }}
    </style>
</head>
<body>
    <div class="label">VISITORS</div>
    <pre>
{chr(10).join(ascii_lines)}
    </pre>
    <div class="stats">
        Unique: {len(state["unique_visitors"])} |
        You are: {visitor_ip} |
        <a href="/stats" style="color:#0f0">JSON stats</a>
    </div>
</body>
</html>
"""
        self.send_response(200)
        self.send_header("Content-Type", "text/html")
        self.end_headers()
        self.wfile.write(response.encode())


if __name__ == "__main__":
    port = int(os.environ.get("PORT", 8080))
    server = HTTPServer(("0.0.0.0", port), CounterHandler)
    print(f"Hit counter running on port {port}")
    print(f"Visit http://localhost:{port}")
    print(f"Stats at http://localhost:{port}/stats")
    server.serve_forever()
