#!/usr/bin/env python3
"""LangGraph POC validating the MCP callback mechanism (CW-20260813-0012).

A small, hand-authored LangGraph graph — built directly with LangGraph's own
node/edge/state API, not compiled from any Nanite-side workflow schema (design
doc, "POC scope"). Every node that needs to do real work reaches Nanite's
callback tools (workflow_execute_tool_step, workflow_execute_llm_step,
workflow_verify_step) through langchain-mcp-adapters instead of calling a
model client directly, proving an external framework's own native graph can
route all real work back through Nanite's harness — the callback mechanism
CW-20260813-0011 built. This is a POC: small and demonstrative, not
production-shaped.

Graph shape (one node per step kind, per the design doc's three-kind
vocabulary):

    START -> tool_step -> llm_step -> verify_step -> END

  - tool_step  (StepKindTool): workflow_execute_tool_step, engine-owned,
    never touches an LLM.
  - llm_step   (StepKindLLM): workflow_execute_llm_step, a capability-
    restricted agent turn.
  - verify_step (the `verify` modifier, mode=engine): workflow_verify_step
    checking the tool step's literal output — deterministic, no LLM.

Usage:    python3 langgraph_poc.py <input.json path>
Requires: pip install -r requirements.txt

Mirrors smoke_test.py's environment overrides and its scoping note on
workflow_execute_llm_step: a provider without real credentials configured on
the target Nanite instance still returns a structured CallToolResult (not a
transport exception), which is enough to prove the callback round trip
(LangGraph node -> langchain-mcp-adapters -> MCP -> self-tool ->
StepExecutor) — see the llm_step node below.
"""
import asyncio
import json
import os
import sys
from typing import Any, TypedDict

from langchain_mcp_adapters.client import MultiServerMCPClient
from langgraph.graph import END, START, StateGraph


class WorkflowState(TypedDict, total=False):
    workflow_input: dict[str, Any]
    tool_result: dict[str, Any]
    llm_result: dict[str, Any]
    verify_result: dict[str, Any]


def parse_tool_output(raw: Any) -> dict[str, Any]:
    """Unwrap a workflow_* callback tool's call result into its JSON body.

    A LangChain tool built by langchain-mcp-adapters returns a list of
    ToolMessageContentBlock dicts (`{"type": "text", "text": ...}`) from
    `.ainvoke()`; the workflow_* tools' text bodies are themselves JSON
    (self_tools_workflow.go's jsonToolResult), so one more json.loads
    recovers the real {output, is_error} / {text, tool_calls, ...} /
    {passed, reason, ...} shape a node routes on.
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


def build_graph(tools_by_name: dict[str, Any]):
    tool_step_tool = tools_by_name["workflow_execute_tool_step"]
    llm_step_tool = tools_by_name["workflow_execute_llm_step"]
    verify_step_tool = tools_by_name["workflow_verify_step"]

    async def tool_step(state: WorkflowState) -> dict[str, Any]:
        # StepKindTool: engine-owned call, the literal result feeds forward
        # — an LLM never sees or chooses this call (design doc).
        raw = await tool_step_tool.ainvoke({"tool": "tool_list", "args": {}})
        return {"tool_result": parse_tool_output(raw)}

    async def llm_step(state: WorkflowState) -> dict[str, Any]:
        # StepKindLLM: a capability-restricted agent turn. tools=[] here —
        # this step does no tool-calling of its own, it just proves an LLM
        # turn can be driven through the harness from a LangGraph node.
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
        return {"llm_result": parse_tool_output(raw)}

    async def verify_step(state: WorkflowState) -> dict[str, Any]:
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
        return {"verify_result": parse_tool_output(raw)}

    graph = StateGraph(WorkflowState)
    graph.add_node("tool_step", tool_step)
    graph.add_node("llm_step", llm_step)
    graph.add_node("verify_step", verify_step)
    graph.add_edge(START, "tool_step")
    graph.add_edge("tool_step", "llm_step")
    graph.add_edge("llm_step", "verify_step")
    graph.add_edge("verify_step", END)
    return graph.compile()


async def main() -> int:
    if len(sys.argv) < 2:
        print("usage: langgraph_poc.py <input.json path>", file=sys.stderr)
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
    # per tool call, exactly like a real LangGraph/CrewAI integration would
    # use it — deliberately not the hand-rolled single-session stdio_client
    # smoke_test.py uses, to prove the idiomatic external-framework path
    # too.
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

    app = build_graph(tools_by_name)
    final_state = await app.ainvoke({"workflow_input": workflow_input})

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
