#!/usr/bin/env python3
"""LangChain production runner for Nanite's Agent Workflows "langchain"
engine (CW-20260814-0007). Extends the external-engine mechanism
(CW-20260813-0011/CW-20260814-0003) with plain LangChain — deliberately
distinct from langgraph_runner.py's LangGraph engine, and using none of
LangGraph's API. A WorkflowDefinition with Engine: "langchain"
(agentworkflow.EngineLangChain) runs exactly this hand-authored chain;
there is still no compiler from Nanite's own workflow-definition format to
LangChain's (design doc, "POC scope" — that stays a non-goal). Embedded
into the nanite binary via internal/workflowrunner's go:embed and
materialized to disk at startup (internal/workflowrunner.MaterializeScripts)
so a Cerberus-deployed binary — which ships alone, with no guarantee a repo
checkout sits alongside it — can still locate it.

Why LCEL instead of a graph: LangGraph already covers the graph-shaped
authoring style (langgraph_runner.py). Plain LangChain's own native
composition primitive is LCEL (LangChain Expression Language) — Runnables
piped together with `|` into a RunnableSequence — not a second graph
wrapper. Unlike CrewAI/ADK/AutoGen's Agent abstractions, an LCEL Runnable
never needs "some LLM" of its own to drive a tool-calling loop: a
RunnableLambda is just an async function, so there is no analog to those
frameworks' deterministic-stub-model problem here. The one real model
call in this run — the llm_step — happens entirely inside the
workflow_execute_llm_step callback tool; no LangChain-native chat model is
ever constructed. This is a legitimately different, equally native
LangChain authoring style from LangGraph's graph API, not a downgrade of
it.

Chain shape (one RunnableLambda per step kind, matching the LangGraph/
CrewAI POCs' shape for parity):

    tool_step | llm_step | verify_step   (RunnableSequence via LCEL `|`)

  - tool_step  (StepKindTool): workflow_execute_tool_step, engine-owned,
    never touches an LLM.
  - llm_step   (StepKindLLM): workflow_execute_llm_step, a capability-
    restricted agent turn.
  - verify_step (the `verify` modifier, mode=engine): workflow_verify_step
    checking the tool step's literal output — deterministic, no LLM.

Usage:    python3 langchain_runner.py <input.json path>
Requires: pip install -r requirements.txt

Mirrors smoke_test.py's environment overrides and its scoping note on
workflow_execute_llm_step: a provider without real credentials configured on
the target Nanite instance still returns a structured CallToolResult (not a
transport exception), which is enough to prove the callback round trip
(LangChain Runnable -> langchain-mcp-adapters -> MCP -> self-tool ->
StepExecutor) — see the llm_step node below.
"""
import asyncio
import json
import os
import sys
from typing import Any

from langchain_core.runnables import RunnableLambda
from langchain_mcp_adapters.client import MultiServerMCPClient


def parse_tool_output(raw: Any) -> dict[str, Any]:
    """Unwrap a workflow_* callback tool's call result into its JSON body.

    A LangChain tool built by langchain-mcp-adapters returns a list of
    ToolMessageContentBlock dicts (`{"type": "text", "text": ...}`) from
    `.ainvoke()`; the workflow_* tools' text bodies are themselves JSON
    (self_tools_workflow.go's jsonToolResult), so one more json.loads
    recovers the real {output, is_error} / {text, tool_calls, ...} /
    {passed, reason, ...} shape a step routes on. Identical to
    langgraph_runner.py's helper of the same name — the underlying
    langchain-mcp-adapters tool objects return the same shape regardless
    of whether LangGraph or plain LangChain invokes them.
    """
    if isinstance(raw, list):
        parts = []
        for block in raw:
            if isinstance(block, dict):
                parts.append(block.get("text", ""))
            else:
                parts.append(getattr(block, "text", str(block)))
        raw = "".join(parts)
    return json.loads(raw)


def build_chain(tools_by_name: dict[str, Any]):
    tool_step_tool = tools_by_name["workflow_execute_tool_step"]
    llm_step_tool = tools_by_name["workflow_execute_llm_step"]
    verify_step_tool = tools_by_name["workflow_verify_step"]

    async def tool_step(state: dict[str, Any]) -> dict[str, Any]:
        # StepKindTool: engine-owned call, the literal result feeds forward
        # — an LLM never sees or chooses this call (design doc).
        raw = await tool_step_tool.ainvoke({"tool": "tool_list", "args": {}})
        return {**state, "tool_result": parse_tool_output(raw)}

    async def llm_step(state: dict[str, Any]) -> dict[str, Any]:
        # StepKindLLM: a capability-restricted agent turn. tools=[] here —
        # this step does no tool-calling of its own, it just proves an LLM
        # turn can be driven through the harness from a LangChain Runnable.
        provider = os.environ.get("NANITE_TEST_PROVIDER", "anthropic")
        model = os.environ.get("NANITE_TEST_MODEL", "claude-sonnet-5")
        raw = await llm_step_tool.ainvoke(
            {
                "provider": provider,
                "model": model,
                "messages": [
                    {"role": "user", "content": "Reply with the single word: ok"}
                ],
            }
        )
        return {**state, "llm_result": parse_tool_output(raw)}

    async def verify_step(state: dict[str, Any]) -> dict[str, Any]:
        # verify modifier (mode=engine) over the tool step's literal output
        # — deterministic, no LLM involved (design doc: "verify is a
        # modifier on llm/tool steps, not a fourth step kind").
        tool_result = state["tool_result"]
        subject = {
            "step_kind": "tool",
            "output": json.dumps(tool_result),
            "is_error": bool(tool_result.get("is_error", False)),
        }
        raw = await verify_step_tool.ainvoke(
            {"mode": "engine", "engine_check": "no_error", "subject": subject}
        )
        return {**state, "verify_result": parse_tool_output(raw)}

    # LCEL: three RunnableLambdas piped into one RunnableSequence — plain
    # LangChain's own native composition primitive, not a graph.
    return (
        RunnableLambda(tool_step)
        | RunnableLambda(llm_step)
        | RunnableLambda(verify_step)
    )


async def main() -> int:
    if len(sys.argv) < 2:
        print("usage: langchain_runner.py <input.json path>", file=sys.stderr)
        return 2

    input_path = sys.argv[1]
    work_dir = os.path.dirname(os.path.abspath(input_path))

    with open(input_path) as f:
        workflow_input = json.load(f)
    with open(os.path.join(work_dir, ".mcp.json")) as f:
        mcp_config = json.load(f)

    server = mcp_config["mcpServers"]["nanite"]
    client = MultiServerMCPClient(
        {
            "nanite": {
                "transport": "stdio",
                "command": server["command"],
                "args": server["args"],
                "env": {**os.environ, **(server.get("env") or {})},
            }
        }
    )

    # langchain-mcp-adapters' native discovery path: a fresh MCP session
    # per tool call, exactly like langgraph_runner.py uses it — proving the
    # idiomatic external-framework path for plain LangChain too.
    tools = await client.get_tools()
    tools_by_name = {tool.name: tool for tool in tools}
    required = [
        "workflow_execute_tool_step",
        "workflow_execute_llm_step",
        "workflow_verify_step",
    ]
    missing = [name for name in required if name not in tools_by_name]
    if missing:
        print(f"missing required MCP tools: {missing}", file=sys.stderr)
        return 1

    print(f"workflow input: {workflow_input}")

    chain = build_chain(tools_by_name)
    final_state = await chain.ainvoke({"workflow_input": workflow_input})

    print(json.dumps(final_state, indent=2, default=str))

    tool_ok = "tool_result" in final_state and not final_state["tool_result"].get(
        "is_error", True
    )
    # A structured llm_result (even a provider/auth error) proves the
    # round trip — see module docstring and smoke_test.py's identical
    # scoping note.
    llm_ok = "llm_result" in final_state
    verify_ok = "verify_result" in final_state and final_state["verify_result"].get(
        "passed", False
    )

    checks = {"tool_step": tool_ok, "llm_step": llm_ok, "verify_step": verify_ok}
    print(f"checks: {checks}")
    return 0 if all(checks.values()) else 1


if __name__ == "__main__":
    sys.exit(asyncio.run(main()))
