#!/usr/bin/env python3
"""
script.py - Example showing automatic dependency installation.

prisn will automatically detect requirements.txt and install dependencies.

Run with:
    prisn run examples/with-deps/script.py
"""
import requests

# Fetch a simple API
response = requests.get("https://httpbin.org/json")
data = response.json()

print("Fetched data from httpbin.org:")
print(f"  Status: {response.status_code}")
print(f"  Slideshow title: {data['slideshow']['title']}")
