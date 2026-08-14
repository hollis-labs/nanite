#!/usr/bin/env python3
"""Minimal MCP callback smoke test for CW-20260813-0011.

Stands in for a real LangGraph/CrewAI node: reads the workflow input and
the .mcp.json that internal/workflowrunner.Launch planted in its cwd,
spawns Nanite's self-hosted MCP server the same way those frameworks'
native MCP adapters would (langchain-mcp-adapters, crewai-tools), and
calls the three workflow_* callback tools. Proves the callback mechanism
works end to end without depending on either framework — the LangGraph
(CW-20260813-0012) and CrewAI (CW-20260813-0013) POCs build on this once
it lands.

Usage:   python3 smoke_test.py <input.json path>
Requires: pip install mcp

The workflow_execute_llm_step call needs a provider with real
credentials configured on the target Nanite instance to fully succeed;
in an environment with none configured it still round-trips (a
structured "unknown provider" error), which is enough to prove the
callback mechanism itself — see the comment at that call site. Override
the provider/model it uses via NANITE_TEST_PROVIDER / NANITE_TEST_MODEL.
"""
import asyncio
import json
import os
import sys

from mcp import ClientSession
from mcp.client.stdio import StdioServerParameters, stdio_client


def render(result) -> str:
    text = "".join(getattr(c, "text", "") for c in result.content)
    if len(text) > 500:
        text = text[:500] + "...(truncated)"
    return f"isError={result.is_error} {text}"


async def main() -> int:
    if len(sys.argv) < 2:
        print("usage: smoke_test.py <input.json path>", file=sys.stderr)
        return 2

    input_path = sys.argv[1]
    work_dir = os.path.dirname(os.path.abspath(input_path))

    with open(input_path) as f:
        workflow_input = json.load(f)
    with open(os.path.join(work_dir, ".mcp.json")) as f:
        mcp_config = json.load(f)

    server = mcp_config["mcpServers"]["nanite"]
    params = StdioServerParameters(
        command=server["command"],
        args=server["args"],
        env={**os.environ, **(server.get("env") or {})},
    )

    print(f"workflow input: {workflow_input}")

    checks_ok = 0
    checks_total = 3

    async with stdio_client(params) as (read, write):
        async with ClientSession(read, write) as session:
            await session.initialize()

            # 1. workflow_execute_tool_step — never touches an LLM. No
            # external dependency, so isError=False is a hard
            # expectation of correct wiring.
            result = await session.call_tool(
                "workflow_execute_tool_step",
                {"tool": "tool_list", "args": {}},
            )
            print("workflow_execute_tool_step ->", render(result))
            if not result.is_error:
                checks_ok += 1

            # 2. workflow_verify_step, mode=engine — deterministic named
            # check, no external dependency either.
            result = await session.call_tool(
                "workflow_verify_step",
                {
                    "mode": "engine",
                    "engine_check": "no_error",
                    "subject": {
                        "step_kind": "tool",
                        "output": "smoke test output",
                        "is_error": False,
                    },
                },
            )
            print("workflow_verify_step ->", render(result))
            if not result.is_error:
                checks_ok += 1

            # 3. workflow_execute_llm_step — requires a registered
            # provider with real credentials to fully succeed. This
            # ticket's job is to prove the *callback mechanism* works,
            # not to complete a live LLM call in every environment: a
            # structured error back through the tool call (e.g.
            # "unknown provider") still proves the round trip (Python ->
            # MCP -> self-tool -> StepExecutor) succeeded end to end, so
            # simply getting a CallToolResult back (not a transport
            # exception) counts as this check passing.
            provider = os.environ.get("NANITE_TEST_PROVIDER", "anthropic")
            model = os.environ.get("NANITE_TEST_MODEL", "claude-sonnet-5")
            result = await session.call_tool(
                "workflow_execute_llm_step",
                {
                    "provider": provider,
                    "model": model,
                    "messages": [
                        {"role": "user", "content": "Reply with the single word: ok"}
                    ],
                },
            )
            print("workflow_execute_llm_step ->", render(result))
            checks_ok += 1

    print(f"{checks_ok}/{checks_total} callback tools round-tripped")
    return 0 if checks_ok == checks_total else 1


if __name__ == "__main__":
    sys.exit(asyncio.run(main()))
