"""Tests for serve.py's parsing. Needs no GPU, torch or model.

Run with: python3 -m unittest discover -s evals/docs-agent/training
"""

import json
import unittest

from serve import normalize_messages, parse_output, stream_events

TOOLS = [
    {
        "type": "function",
        "function": {
            "name": "code__read",
            "parameters": {
                "properties": {
                    "path": {"type": "string"},
                    "offset": {"type": "integer"},
                }
            },
        },
    }
]

CALL = (
    "<tool_call>\n<function=code__read>\n"
    "<parameter=path>\ninternal/config.go\n</parameter>\n"
    "<parameter=offset>\n190\n</parameter>\n"
    "</function>\n</tool_call>"
)


class ParseOutputTest(unittest.TestCase):
    def test_splits_reasoning_and_content(self):
        reasoning, content, calls = parse_output("look it up</think>The answer.")
        self.assertEqual(reasoning, "look it up")
        self.assertEqual(content, "The answer.")
        self.assertEqual(calls, [])

    def test_unclosed_reasoning_has_no_content(self):
        self.assertEqual(parse_output("still thinking"), ("still thinking", "", []))

    def test_no_thinking_keeps_all_text_as_content(self):
        self.assertEqual(parse_output("Hi.", thinking=False), ("", "Hi.", []))

    def test_tool_call_uses_schema_types(self):
        _, content, calls = parse_output(f"plan</think>{CALL}", TOOLS)
        self.assertEqual(content, "")
        self.assertEqual(calls[0]["function"]["name"], "code__read")
        self.assertEqual(
            json.loads(calls[0]["function"]["arguments"]),
            {"path": "internal/config.go", "offset": 190},
        )

    def test_string_parameter_stays_a_string(self):
        schema = [
            {"function": {"name": "s", "parameters": {"properties": {"q": {}}}}}
        ]
        text = "<tool_call>\n<function=s>\n<parameter=q>\n123\n</parameter>\n</function>\n</tool_call>"
        _, _, calls = parse_output(text, schema, thinking=False)
        self.assertEqual(json.loads(calls[0]["function"]["arguments"]), {"q": "123"})

    def test_multiple_tool_calls_are_indexed(self):
        _, _, calls = parse_output(f"{CALL}\n\n{CALL}", TOOLS, thinking=False)
        self.assertEqual([c["index"] for c in calls], [0, 1])

    def test_truncated_tool_call_stays_in_content(self):
        text = "<tool_call>\n<function=code__read>\n"
        self.assertEqual(parse_output(text, TOOLS, thinking=False), ("", text.strip(), []))


class NormalizeMessagesTest(unittest.TestCase):
    def test_decodes_json_arguments(self):
        message = {
            "role": "assistant",
            "tool_calls": [{"function": {"name": "x", "arguments": '{"a": 1}'}}],
        }
        out = normalize_messages([message])
        self.assertEqual(out[0]["tool_calls"][0]["function"]["arguments"], {"a": 1})
        self.assertIsInstance(message["tool_calls"][0]["function"]["arguments"], str)

    def test_flattens_text_parts(self):
        message = {"role": "user", "content": [{"type": "text", "text": "hi"}]}
        self.assertEqual(normalize_messages([message])[0]["content"], "hi")


class StreamEventsTest(unittest.TestCase):
    def test_tool_calls_set_finish_reason_and_usage_comes_last(self):
        call = {"index": 0, "id": "c", "type": "function", "function": {}}
        result = {
            "reasoning": "r",
            "content": "",
            "calls": [call],
            "usage": {"total_tokens": 3},
        }
        events = list(stream_events(result, "m"))
        self.assertEqual(events[-1]["usage"], {"total_tokens": 3})
        self.assertEqual(events[-2]["choices"][0]["finish_reason"], "tool_calls")


if __name__ == "__main__":
    unittest.main()
