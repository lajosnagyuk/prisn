#!/usr/bin/env python3
"""
hello.py - The simplest possible prisn example.

Run with:
    prisn run hello.py
"""
import os

name = os.environ.get("NAME", "World")
print(f"Hello, {name}!")
