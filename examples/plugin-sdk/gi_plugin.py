"""Gi Plugin SDK for Python.

A lightweight single-file module for building Gi plugins. Handles the JSONL
protocol, init/capabilities handshake, and message routing so plugin authors
can focus on tool and command logic.

Example::

    from gi_plugin import Plugin

    plugin = Plugin("my-plugin")

    @plugin.tool("greet", "Say hello", {
        "type": "object",
        "properties": {"name": {"type": "string", "description": "Name to greet"}},
        "required": ["name"],
    })
    def greet(params, config):
        return f"Hello, {params['name']}!"

    @plugin.command("status", "Show status")
    def status(args, config):
        return "all good"

    @plugin.event
    def on_event(event):
        pass  # fire-and-forget

    plugin.run()
"""

from __future__ import annotations

import json
import sys
from dataclasses import dataclass, field
from typing import Any, Callable

__all__ = ["Plugin"]


@dataclass
class Config:
    """Plugin configuration received during initialization."""
    cwd: str = ""
    model: str = ""
    provider: str = ""
    gi_version: str = ""


@dataclass
class _ToolEntry:
    name: str
    description: str
    input_schema: Any
    handler: Callable


@dataclass
class _CommandEntry:
    name: str
    description: str
    handler: Callable


class Plugin:
    """Main entry point for building a Gi plugin.

    Usage::

        plugin = Plugin("my-plugin")

        @plugin.tool("greet", "Say hello", schema)
        def greet(params, config):
            return f"Hello, {params.get('name', 'world')}!"

        plugin.run()
    """

    def __init__(self, name: str) -> None:
        self.name = name
        self.config = Config()
        self._tools: list[_ToolEntry] = []
        self._commands: list[_CommandEntry] = []
        self._event_handlers: list[Callable] = []
        self._init_handler: Callable | None = None

    def tool(
        self,
        name: str,
        description: str,
        input_schema: Any = None,
    ) -> Callable:
        """Decorator to register a tool handler.

        The decorated function receives ``(params: dict, config: Config)``
        and should return a string result. Raise an exception to signal an error.

        Args:
            name: Tool name (must be unique).
            description: Human-readable description shown to the LLM.
            input_schema: JSON Schema object describing the tool's parameters.
        """
        if input_schema is None:
            input_schema = {"type": "object", "properties": {}}

        def decorator(fn: Callable) -> Callable:
            self._tools.append(_ToolEntry(name, description, input_schema, fn))
            return fn

        return decorator

    def command(self, name: str, description: str) -> Callable:
        """Decorator to register a command handler.

        The decorated function receives ``(args: str, config: Config)``
        and should return a string result. Raise an exception to signal an error.

        Args:
            name: Command name (slash command, e.g. "greet" for /greet).
            description: Human-readable description.
        """

        def decorator(fn: Callable) -> Callable:
            self._commands.append(_CommandEntry(name, description, fn))
            return fn

        return decorator

    def event(self, fn: Callable) -> Callable:
        """Decorator to register an event handler.

        The decorated function receives a single ``event: dict`` argument.
        Events are fire-and-forget; no response is sent.

        Can be used multiple times to register multiple handlers.
        """
        self._event_handlers.append(fn)
        return fn

    def on_init(self, fn: Callable) -> Callable:
        """Decorator to register an initialization handler.

        The decorated function receives ``(config: Config)`` and is called
        after the initialize message is received but before capabilities
        are sent. Raise an exception to abort startup.
        """
        self._init_handler = fn
        return fn

    def inject(self, role: str, content: str) -> None:
        """Send an inject_message to add context to the conversation."""
        self._send({"type": "inject_message", "role": role, "content": content})

    def log(self, level: str, message: str) -> None:
        """Send a log message to the host."""
        self._send({"type": "log", "level": level, "message": message})

    def run(self, *, input_stream=None, output_stream=None) -> None:
        """Start the plugin message loop.

        Blocks until stdin is closed or a shutdown message is received.
        Handles the init/capabilities handshake automatically.

        Args:
            input_stream: Override stdin (for testing).
            output_stream: Override stdout (for testing).
        """
        inp = input_stream or sys.stdin
        out = output_stream or sys.stdout
        self._output = out

        # Wait for initialize.
        line = inp.readline()
        if not line:
            return

        init_msg = json.loads(line)
        if init_msg.get("type") != "initialize":
            self.log("error", f"expected initialize, got {init_msg.get('type')}")
            return

        cfg = init_msg.get("config", {})
        self.config = Config(
            cwd=cfg.get("cwd", ""),
            model=cfg.get("model", ""),
            provider=cfg.get("provider", ""),
            gi_version=cfg.get("gi_version", ""),
        )

        # Call init handler if registered.
        if self._init_handler is not None:
            self._init_handler(self.config)

        # Send capabilities.
        caps: dict[str, Any] = {"type": "capabilities"}
        if self._tools:
            caps["tools"] = [
                {
                    "name": t.name,
                    "description": t.description,
                    "input_schema": t.input_schema,
                }
                for t in self._tools
            ]
        if self._commands:
            caps["commands"] = [
                {"name": c.name, "description": c.description}
                for c in self._commands
            ]
        self._send(caps)

        self.log(
            "info",
            f"{self.name} initialized ({len(self._tools)} tools, {len(self._commands)} commands)",
        )

        # Main message loop.
        for line in inp:
            line = line.strip()
            if not line:
                continue
            try:
                msg = json.loads(line)
            except json.JSONDecodeError:
                self.log("warn", f"failed to parse message: {line[:100]}")
                continue
            self._dispatch(msg)

    def _dispatch(self, msg: dict) -> None:
        msg_type = msg.get("type")
        if msg_type == "tool_call":
            self._handle_tool_call(msg)
        elif msg_type == "command":
            self._handle_command(msg)
        elif msg_type == "event":
            self._handle_event(msg)
        elif msg_type == "shutdown":
            sys.exit(0)

    def _handle_tool_call(self, msg: dict) -> None:
        call_id = msg.get("id", "")
        name = msg.get("name", "")
        params = msg.get("params", {})

        for t in self._tools:
            if t.name == name:
                try:
                    result = t.handler(params, self.config)
                    self._send({
                        "type": "tool_result",
                        "id": call_id,
                        "content": str(result),
                    })
                except Exception as exc:
                    self._send({
                        "type": "tool_result",
                        "id": call_id,
                        "content": str(exc),
                        "is_error": True,
                    })
                return

        self._send({
            "type": "tool_result",
            "id": call_id,
            "content": f"unknown tool: {name}",
            "is_error": True,
        })

    def _handle_command(self, msg: dict) -> None:
        name = msg.get("name", "")
        args = msg.get("args", "")

        for c in self._commands:
            if c.name == name:
                try:
                    result = c.handler(args, self.config)
                    self._send({"type": "command_result", "text": str(result)})
                except Exception as exc:
                    self._send({
                        "type": "command_result",
                        "text": str(exc),
                        "is_error": True,
                    })
                return

        self._send({
            "type": "command_result",
            "text": f"unknown command: {name}",
            "is_error": True,
        })

    def _handle_event(self, msg: dict) -> None:
        event = msg.get("event")
        if event is None:
            return
        for handler in self._event_handlers:
            handler(event)

    def _send(self, msg: dict) -> None:
        out = getattr(self, "_output", sys.stdout)
        print(json.dumps(msg, separators=(",", ":")), file=out, flush=True)
