#!/usr/bin/env python3
"""
Guestbook - A simple message board web app.

Deploy: prisn deploy examples/guestbook.py --name guestbook --port 8080
Usage:
    POST /messages  {"name": "Alice", "message": "Hello!"}
    GET  /messages  List all messages
    GET  /          Web UI
"""
from http.server import HTTPServer, BaseHTTPRequestHandler
import json
import os
from datetime import datetime
from urllib.parse import parse_qs

# In-memory storage (resets on restart)
messages = []


class GuestbookHandler(BaseHTTPRequestHandler):
    def log_message(self, format, *args):
        pass

    def do_GET(self):
        if self.path == "/messages":
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Access-Control-Allow-Origin", "*")
            self.end_headers()
            self.wfile.write(json.dumps(messages).encode())
            return

        if self.path == "/health":
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.end_headers()
            self.wfile.write(json.dumps({"status": "ok", "messages": len(messages)}).encode())
            return

        # Serve the web UI
        html = f"""
<!DOCTYPE html>
<html>
<head>
    <title>Guestbook</title>
    <style>
        * {{ box-sizing: border-box; margin: 0; padding: 0; }}
        body {{
            font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
            background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
            min-height: 100vh;
            padding: 20px;
        }}
        .container {{
            max-width: 600px;
            margin: 0 auto;
        }}
        h1 {{
            color: white;
            text-align: center;
            margin-bottom: 20px;
            text-shadow: 2px 2px 4px rgba(0,0,0,0.3);
        }}
        .form-card {{
            background: white;
            border-radius: 12px;
            padding: 20px;
            margin-bottom: 20px;
            box-shadow: 0 10px 40px rgba(0,0,0,0.2);
        }}
        input, textarea {{
            width: 100%;
            padding: 12px;
            margin: 8px 0;
            border: 2px solid #eee;
            border-radius: 8px;
            font-size: 16px;
        }}
        input:focus, textarea:focus {{
            outline: none;
            border-color: #667eea;
        }}
        button {{
            width: 100%;
            padding: 14px;
            background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
            color: white;
            border: none;
            border-radius: 8px;
            font-size: 16px;
            cursor: pointer;
            margin-top: 10px;
        }}
        button:hover {{ opacity: 0.9; }}
        .messages {{
            margin-top: 20px;
        }}
        .message {{
            background: white;
            border-radius: 12px;
            padding: 15px;
            margin-bottom: 10px;
            box-shadow: 0 4px 15px rgba(0,0,0,0.1);
        }}
        .message-header {{
            display: flex;
            justify-content: space-between;
            margin-bottom: 8px;
        }}
        .message-name {{
            font-weight: bold;
            color: #667eea;
        }}
        .message-time {{
            color: #999;
            font-size: 12px;
        }}
        .message-text {{
            color: #333;
            line-height: 1.5;
        }}
        .stats {{
            text-align: center;
            color: white;
            opacity: 0.8;
            margin-top: 20px;
        }}
    </style>
</head>
<body>
    <div class="container">
        <h1>Guestbook</h1>

        <div class="form-card">
            <form id="messageForm">
                <input type="text" id="name" placeholder="Your name" required maxlength="50">
                <textarea id="message" placeholder="Leave a message..." rows="3" required maxlength="500"></textarea>
                <button type="submit">Post Message</button>
            </form>
        </div>

        <div class="messages" id="messages">
            Loading messages...
        </div>

        <div class="stats">
            Powered by <strong>prisn</strong> | {len(messages)} messages
        </div>
    </div>

    <script>
        async function loadMessages() {{
            const res = await fetch('/messages');
            const messages = await res.json();
            const container = document.getElementById('messages');

            if (messages.length === 0) {{
                container.innerHTML = '<div class="message"><div class="message-text">No messages yet. Be the first to sign!</div></div>';
                return;
            }}

            container.innerHTML = messages.reverse().map(m => `
                <div class="message">
                    <div class="message-header">
                        <span class="message-name">${{escapeHtml(m.name)}}</span>
                        <span class="message-time">${{m.time}}</span>
                    </div>
                    <div class="message-text">${{escapeHtml(m.message)}}</div>
                </div>
            `).join('');
        }}

        function escapeHtml(text) {{
            const div = document.createElement('div');
            div.textContent = text;
            return div.innerHTML;
        }}

        document.getElementById('messageForm').onsubmit = async (e) => {{
            e.preventDefault();
            const name = document.getElementById('name').value;
            const message = document.getElementById('message').value;

            await fetch('/messages', {{
                method: 'POST',
                headers: {{ 'Content-Type': 'application/json' }},
                body: JSON.stringify({{ name, message }})
            }});

            document.getElementById('message').value = '';
            loadMessages();
        }};

        loadMessages();
        setInterval(loadMessages, 5000);  // Refresh every 5s
    </script>
</body>
</html>
"""
        self.send_response(200)
        self.send_header("Content-Type", "text/html")
        self.end_headers()
        self.wfile.write(html.encode())

    def do_POST(self):
        if self.path == "/messages":
            content_length = int(self.headers.get("Content-Length", 0))
            body = self.rfile.read(content_length).decode()

            try:
                data = json.loads(body)
                name = data.get("name", "Anonymous")[:50]
                message = data.get("message", "")[:500]

                if not message.strip():
                    self.send_response(400)
                    self.send_header("Content-Type", "application/json")
                    self.end_headers()
                    self.wfile.write(json.dumps({"error": "Message required"}).encode())
                    return

                entry = {
                    "name": name,
                    "message": message,
                    "time": datetime.now().strftime("%Y-%m-%d %H:%M"),
                    "ip": self.client_address[0],
                }
                messages.append(entry)

                # Keep only last 100 messages
                if len(messages) > 100:
                    messages.pop(0)

                self.send_response(201)
                self.send_header("Content-Type", "application/json")
                self.send_header("Access-Control-Allow-Origin", "*")
                self.end_headers()
                self.wfile.write(json.dumps(entry).encode())

            except json.JSONDecodeError:
                self.send_response(400)
                self.send_header("Content-Type", "application/json")
                self.end_headers()
                self.wfile.write(json.dumps({"error": "Invalid JSON"}).encode())

    def do_OPTIONS(self):
        self.send_response(200)
        self.send_header("Access-Control-Allow-Origin", "*")
        self.send_header("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
        self.send_header("Access-Control-Allow-Headers", "Content-Type")
        self.end_headers()


if __name__ == "__main__":
    port = int(os.environ.get("PORT", 8080))
    server = HTTPServer(("0.0.0.0", port), GuestbookHandler)
    print(f"Guestbook running on port {port}")
    print(f"Web UI:   http://localhost:{port}")
    print(f"API:      POST /messages")
    server.serve_forever()
