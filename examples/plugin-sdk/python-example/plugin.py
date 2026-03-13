#!/usr/bin/env python3
"""Example Gi plugin using the Python SDK.

This plugin provides a "word_count" tool and a "wc" command.

Install:
    cp gi_plugin.py plugin.py ~/.gi/plugins/word-counter/
    chmod +x ~/.gi/plugins/word-counter/plugin.py
"""

import sys
import os

# Add parent directory so gi_plugin can be imported when installed alongside.
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
# Also check one level up for development layout.
sys.path.insert(0, os.path.join(os.path.dirname(os.path.abspath(__file__)), ".."))

from gi_plugin import Plugin

plugin = Plugin("word-counter")


@plugin.tool("word_count", "Count words in text", {
    "type": "object",
    "properties": {
        "text": {"type": "string", "description": "The text to count words in"},
    },
    "required": ["text"],
})
def word_count(params, config):
    text = params.get("text", "")
    words = text.split()
    return f"{len(words)} words"


@plugin.command("wc", "Count words in the given text")
def wc(args, config):
    words = args.split()
    return f"{len(words)} words"


@plugin.event
def on_event(event):
    """Observe agent events (fire-and-forget, no response needed)."""
    pass


if __name__ == "__main__":
    plugin.run()
