#!/usr/bin/env python3
"""Generate postman_collection.json from the routes registered in the Go code.

Reads the registered routes and creates the complete 32-folder, 224+ request
collection covering all endpoints (Authentication, Recommendations, Compare Venues,
Bookings, Quotes, Help Center, Admin, etc.).

Usage:
  python3 scripts/gen_postman.py > postman_collection.json
  or
  node scripts/gen_postman.js
  or
  make postman
"""
import os
import sys
import subprocess

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
JS_SCRIPT = os.path.join(ROOT, "scripts", "gen_postman.js")
COL_PATH = os.path.join(ROOT, "postman_collection.json")

def main():
    # Run the generator script
    subprocess.check_call(["node", JS_SCRIPT], stderr=sys.stderr)
    
    # If invoked with stdout redirection or argument, stream the JSON content
    target_path = sys.argv[1] if len(sys.argv) > 1 else COL_PATH
    if os.path.exists(target_path):
        with open(target_path, "r", encoding="utf-8") as f:
            content = f.read()
            if not sys.stdout.isatty():
                sys.stdout.write(content)

if __name__ == "__main__":
    main()
