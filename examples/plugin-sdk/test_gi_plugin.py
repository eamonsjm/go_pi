"""Tests for the Gi Plugin Python SDK."""

import io
import json
import unittest

from gi_plugin import Plugin, Config


def make_streams(*messages):
    """Create an input stream from a list of message dicts."""
    lines = [json.dumps(m) + "\n" for m in messages]
    return io.StringIO("".join(lines))


def parse_output(output_stream):
    """Parse all JSONL messages from an output stream."""
    output_stream.seek(0)
    messages = []
    for line in output_stream:
        line = line.strip()
        if line:
            messages.append(json.loads(line))
    return messages


class TestCapabilitiesHandshake(unittest.TestCase):
    def test_basic_handshake(self):
        plugin = Plugin("test")

        @plugin.tool("mytool", "A tool", {"type": "object"})
        def mytool(params, config):
            return "ok"

        @plugin.command("mycmd", "A command")
        def mycmd(args, config):
            return "ok"

        inp = make_streams(
            {"type": "initialize", "config": {"cwd": "/tmp", "model": "test"}},
            {"type": "shutdown"},
        )
        out = io.StringIO()

        with self.assertRaises(SystemExit):
            plugin.run(input_stream=inp, output_stream=out)

        msgs = parse_output(out)
        caps = msgs[0]
        self.assertEqual(caps["type"], "capabilities")
        self.assertEqual(len(caps["tools"]), 1)
        self.assertEqual(caps["tools"][0]["name"], "mytool")
        self.assertEqual(len(caps["commands"]), 1)
        self.assertEqual(caps["commands"][0]["name"], "mycmd")


class TestToolCall(unittest.TestCase):
    def test_tool_call_success(self):
        plugin = Plugin("test")

        @plugin.tool("reverse", "Reverse text", {
            "type": "object",
            "properties": {"text": {"type": "string"}},
            "required": ["text"],
        })
        def reverse(params, config):
            return params.get("text", "")[::-1]

        inp = make_streams(
            {"type": "initialize", "config": {}},
            {"type": "tool_call", "id": "call_1", "name": "reverse", "params": {"text": "hello"}},
            {"type": "shutdown"},
        )
        out = io.StringIO()

        with self.assertRaises(SystemExit):
            plugin.run(input_stream=inp, output_stream=out)

        msgs = parse_output(out)
        # Find tool_result (skip capabilities and log)
        results = [m for m in msgs if m["type"] == "tool_result"]
        self.assertEqual(len(results), 1)
        self.assertEqual(results[0]["id"], "call_1")
        self.assertEqual(results[0]["content"], "olleh")
        self.assertNotIn("is_error", results[0])

    def test_tool_call_error(self):
        plugin = Plugin("test")

        @plugin.tool("fail", "Always fails")
        def fail(params, config):
            raise ValueError("something went wrong")

        inp = make_streams(
            {"type": "initialize", "config": {}},
            {"type": "tool_call", "id": "call_2", "name": "fail"},
            {"type": "shutdown"},
        )
        out = io.StringIO()

        with self.assertRaises(SystemExit):
            plugin.run(input_stream=inp, output_stream=out)

        msgs = parse_output(out)
        results = [m for m in msgs if m["type"] == "tool_result"]
        self.assertEqual(len(results), 1)
        self.assertTrue(results[0]["is_error"])
        self.assertEqual(results[0]["content"], "something went wrong")

    def test_unknown_tool(self):
        plugin = Plugin("test")

        inp = make_streams(
            {"type": "initialize", "config": {}},
            {"type": "tool_call", "id": "call_3", "name": "nonexistent"},
            {"type": "shutdown"},
        )
        out = io.StringIO()

        with self.assertRaises(SystemExit):
            plugin.run(input_stream=inp, output_stream=out)

        msgs = parse_output(out)
        results = [m for m in msgs if m["type"] == "tool_result"]
        self.assertEqual(len(results), 1)
        self.assertTrue(results[0]["is_error"])
        self.assertIn("unknown tool", results[0]["content"])


class TestCommandExecution(unittest.TestCase):
    def test_command_success(self):
        plugin = Plugin("test")

        @plugin.command("greet", "Greet someone")
        def greet(args, config):
            return f"Hello {args}"

        inp = make_streams(
            {"type": "initialize", "config": {}},
            {"type": "command", "name": "greet", "args": "world"},
            {"type": "shutdown"},
        )
        out = io.StringIO()

        with self.assertRaises(SystemExit):
            plugin.run(input_stream=inp, output_stream=out)

        msgs = parse_output(out)
        results = [m for m in msgs if m["type"] == "command_result"]
        self.assertEqual(len(results), 1)
        self.assertEqual(results[0]["text"], "Hello world")


class TestEventHandling(unittest.TestCase):
    def test_events_received(self):
        received = []
        plugin = Plugin("test")

        @plugin.tool("ping", "ping")
        def ping(params, config):
            return "pong"

        @plugin.event
        def on_event(event):
            received.append(event["type"])

        inp = make_streams(
            {"type": "initialize", "config": {}},
            {"type": "event", "event": {"type": "agent_start"}},
            {"type": "event", "event": {"type": "tool_exec_start", "tool_name": "bash"}},
            {"type": "tool_call", "id": "sync", "name": "ping"},
            {"type": "shutdown"},
        )
        out = io.StringIO()

        with self.assertRaises(SystemExit):
            plugin.run(input_stream=inp, output_stream=out)

        self.assertEqual(received, ["agent_start", "tool_exec_start"])


class TestOnInit(unittest.TestCase):
    def test_init_handler(self):
        captured = {}
        plugin = Plugin("test")

        @plugin.on_init
        def init(config):
            captured["cwd"] = config.cwd
            captured["model"] = config.model

        inp = make_streams(
            {"type": "initialize", "config": {"cwd": "/myproject", "model": "opus"}},
            {"type": "shutdown"},
        )
        out = io.StringIO()

        with self.assertRaises(SystemExit):
            plugin.run(input_stream=inp, output_stream=out)

        self.assertEqual(captured["cwd"], "/myproject")
        self.assertEqual(captured["model"], "opus")


class TestInjectAndLog(unittest.TestCase):
    def test_inject_message(self):
        plugin = Plugin("test")

        @plugin.on_init
        def init(config):
            plugin.inject("user", "context info here")

        inp = make_streams(
            {"type": "initialize", "config": {}},
            {"type": "shutdown"},
        )
        out = io.StringIO()

        with self.assertRaises(SystemExit):
            plugin.run(input_stream=inp, output_stream=out)

        msgs = parse_output(out)
        injects = [m for m in msgs if m["type"] == "inject_message"]
        self.assertEqual(len(injects), 1)
        self.assertEqual(injects[0]["role"], "user")
        self.assertEqual(injects[0]["content"], "context info here")


if __name__ == "__main__":
    unittest.main()
