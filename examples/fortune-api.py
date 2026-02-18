#!/usr/bin/env python3
"""
Fortune Cookie API - Get random wisdom, programming quotes, and jokes.

Deploy: prisn deploy examples/fortune-api.py --name fortune --port 8080
Usage:
    curl localhost:8080              # Random fortune
    curl localhost:8080/programming  # Programming quotes
    curl localhost:8080/wisdom       # Ancient wisdom
    curl localhost:8080/dad-jokes    # Dad jokes
"""
from http.server import HTTPServer, BaseHTTPRequestHandler
import json
import random
import os

FORTUNES = {
    "programming": [
        "There are only two hard things in Computer Science: cache invalidation and naming things.",
        "It works on my machine. Ship your machine then.",
        "99 little bugs in the code, 99 bugs in the code. Take one down, patch it around... 127 bugs in the code.",
        "A good programmer is someone who always looks both ways before crossing a one-way street.",
        "First, solve the problem. Then, write the code.",
        "Deleted code is debugged code.",
        "Measuring programming progress by lines of code is like measuring aircraft building progress by weight.",
        "The best error message is the one that never shows up.",
        "Weeks of coding can save you hours of planning.",
        "chmod 777 - the answer to all permissions problems and the start of all security problems.",
        "git push --force: Because history is written by the victors.",
        "SELECT * FROM problems WHERE solution = 'StackOverflow'",
        "console.log('here') // TODO: remove this",
        "Documentation is like sex: when it's good, it's very good; when it's bad, it's better than nothing.",
        "The code works, don't touch it. - Ancient Proverb",
    ],
    "wisdom": [
        "The journey of a thousand miles begins with a single step. - Lao Tzu",
        "In the middle of difficulty lies opportunity. - Albert Einstein",
        "The only true wisdom is knowing you know nothing. - Socrates",
        "Be the change you wish to see in the world. - Gandhi",
        "Life is what happens when you're busy making other plans. - John Lennon",
        "The unexamined life is not worth living. - Socrates",
        "To be yourself in a world that is constantly trying to make you something else is the greatest accomplishment. - Ralph Waldo Emerson",
        "The only thing we have to fear is fear itself. - FDR",
        "Stay hungry, stay foolish. - Steve Jobs",
        "Simplicity is the ultimate sophistication. - Leonardo da Vinci",
    ],
    "dad-jokes": [
        "Why do programmers prefer dark mode? Because light attracts bugs!",
        "I told my wife she was drawing her eyebrows too high. She looked surprised.",
        "Why don't scientists trust atoms? Because they make up everything!",
        "I'm reading a book about anti-gravity. It's impossible to put down!",
        "Why did the scarecrow win an award? Because he was outstanding in his field!",
        "I used to hate facial hair, but then it grew on me.",
        "What do you call a fake noodle? An impasta!",
        "Why don't eggs tell jokes? They'd crack each other up!",
        "I'm on a seafood diet. I see food and I eat it.",
        "What did the ocean say to the beach? Nothing, it just waved.",
        "Why do Java developers wear glasses? Because they can't C#!",
        "How do you organize a space party? You planet.",
    ],
}


class FortuneHandler(BaseHTTPRequestHandler):
    def log_message(self, format, *args):
        pass

    def do_GET(self):
        path = self.path.strip("/").split("?")[0]

        if path in FORTUNES:
            category = path
            fortune = random.choice(FORTUNES[category])
        elif path == "" or path == "random":
            category = random.choice(list(FORTUNES.keys()))
            fortune = random.choice(FORTUNES[category])
        elif path == "categories":
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.end_headers()
            self.wfile.write(json.dumps({
                "categories": list(FORTUNES.keys()),
                "total_fortunes": sum(len(v) for v in FORTUNES.values()),
            }).encode())
            return
        else:
            self.send_response(404)
            self.send_header("Content-Type", "application/json")
            self.end_headers()
            self.wfile.write(json.dumps({
                "error": f"Unknown category: {path}",
                "available": list(FORTUNES.keys()),
            }).encode())
            return

        # Check Accept header for format
        accept = self.headers.get("Accept", "")

        if "application/json" in accept or path.endswith(".json"):
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.end_headers()
            self.wfile.write(json.dumps({
                "fortune": fortune,
                "category": category,
            }).encode())
        else:
            # ASCII art fortune cookie
            cookie = f"""
    _______
   /       \\
  |  {category[:7]:^7}  |
   \\_______/
      | |
   ___| |___
  |         |
  | {fortune[:40]}
  | {fortune[40:80] if len(fortune) > 40 else ''}
  | {fortune[80:120] if len(fortune) > 80 else ''}
  |_________|

"""
            self.send_response(200)
            self.send_header("Content-Type", "text/plain")
            self.end_headers()
            self.wfile.write(cookie.encode())


if __name__ == "__main__":
    port = int(os.environ.get("PORT", 8080))
    server = HTTPServer(("0.0.0.0", port), FortuneHandler)
    print(f"Fortune Cookie API running on port {port}")
    print(f"Endpoints:")
    print(f"  GET /              - Random fortune")
    print(f"  GET /programming   - Programming quotes")
    print(f"  GET /wisdom        - Ancient wisdom")
    print(f"  GET /dad-jokes     - Dad jokes")
    print(f"  GET /categories    - List all categories")
    server.serve_forever()
