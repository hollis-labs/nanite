#!/usr/bin/env python3
"""AutoGen production runner for Nanite's Agent Workflows "autogen" engine
(CW-20260814-0007), extending the external-engine mechanism proven for
LangGraph/CrewAI (CW-20260813-0011/CW-20260814-0003) to a third framework.
Embedded into the nanite binary via internal/workflowrunner's go:embed and
materialized to disk at startup (internal/workflowrunner.MaterializeScripts)
so a Cerberus-deployed binary — which ships alone, with no guarantee a repo
checkout sits alongside it — can still locate it.

"AutoGen" here is the modern Microsoft framework (autogen-agentchat /
autogen-ext / autogen-core, AutoGen 0.4+), not the legacy pyautogen package.

A small, hand-authored set of AutoGen agents — built directly with AutoGen's
own Agent/Tool authoring API (autogen_agentchat.agents.AssistantAgent), not
compiled from any Nanite-side workflow schema (design doc, "POC scope" — no
compiler from Nanite's own workflow-definition format to any external
framework's). Every agent's real work reaches Nanite's callback tools
(workflow_execute_tool_step, workflow_execute_llm_step, workflow_verify_step)
through autogen-ext's own native MCP integration
(autogen_ext.tools.mcp.mcp_server_tools, wrapping StdioServerParams), never
through AutoGen's own model-calling machinery — matching the same discipline
the LangGraph/CrewAI runners already established.

Why a deterministic model_client instead of a real provider-backed one:
every AutoGen AssistantAgent needs *some* ChatCompletionClient to drive its
tool-calling loop — that's intrinsic to the Agent model, not something this
runner can avoid while still "using AutoGen's own native authoring API". But
handing that client real provider credentials would mean AutoGen's own model
layer making real calls directly against a provider, completely bypassing
Nanite — exactly the "second, parallel harness" the design doc's one
principle forbids ("Workflow engines only decide sequencing... every unit of
work... always executes through Nanite's existing harness, never through the
engine itself"). DeterministicToolCallClient has no provider, no
credentials, no network call of its own: its create() deterministically
returns exactly one FunctionCall for the one MCP tool this step is pinned
to. AutoGen's own AssistantAgent.run() executes that call for real (through
the autogen-ext MCP tool adapter, which reaches Nanite's harness) and — with
this runner's default reflect_on_tool_use=False, max_tool_iterations=1 — its
built-in tool_call_summary_format="{result}" relays the tool's literal
result straight through as the run's final message, with no second model
call needed at all. AutoGen's own orchestration layer stays a pure, local,
credential-free sequencer; the autogen-ext MCP integration is what actually
reaches Nanite's harness for every unit of real work — LLM turns included.

Step shape (three agents, one per step kind, matching the LangGraph/CrewAI
runners' shape for parity):

    tool_step -> llm_step -> verify_step   (plain sequential AssistantAgent
                                             .run() calls — AutoGen's own
                                             Team/GroupChat abstractions are
                                             for multi-agent conversations,
                                             not needed for this fixed,
                                             three-callback pipeline)

  - tool_step   (StepKindTool): workflow_execute_tool_step, engine-owned,
    never touches an LLM.
  - llm_step    (StepKindLLM): workflow_execute_llm_step, a capability-
    restricted agent turn — the one REAL model call in this run, made by
    Nanite's harness, not by AutoGen.
  - verify_step (the `verify` modifier, mode=engine): workflow_verify_step
    checking the tool step's literal output — deterministic, no LLM.

Usage:    python3 autogen_runner.py <input.json path>
Requires: pip install -r requirements.txt

Mirrors smoke_test.py's environment overrides and its scoping note on
workflow_execute_llm_step: a provider without real credentials configured on
the target Nanite instance still returns a structured CallToolResult (not a
transport exception), which is enough to prove the callback round trip
(AutoGen agent -> autogen-ext MCP tool adapter -> MCP -> self-tool ->
StepExecutor) — see the llm_step agent below.
"""
import asyncio
import json
import os
import sys
from typing import Any, Callable

from autogen_agentchat.agents import AssistantAgent
from autogen_agentchat.messages import ToolCallSummaryMessage
from autogen_core import FunctionCall
from autogen_core.models import ChatCompletionClient, CreateResult, RequestUsage
from autogen_ext.tools.mcp import StdioServerParams, mcp_server_tools


class DeterministicToolCallClient(ChatCompletionClient):
    """A local, credential-free stand-in for an AssistantAgent's model_client.

    create() deterministically returns exactly one FunctionCall against
    `tool_name`, built fresh each call via `build_arguments` (mirrors
    crewai_runner.py's DeterministicToolCallLLM.build_action_input). AutoGen
    executes that call for real through the bound MCP tool adapter; with
    max_tool_iterations=1 (this runner's default) the agent never calls
    create() a second time — its built-in tool-result summarization relays
    the literal FunctionExecutionResult back as the run's final message. See
    the module docstring for why this exists instead of a real
    provider-backed client.
    """

    def __init__(self, tool_name: str, build_arguments: Callable[[], dict[str, Any]]) -> None:
        self._tool_name = tool_name
        self._build_arguments = build_arguments
        self._usage = RequestUsage(prompt_tokens=0, completion_tokens=0)

    async def create(
        self,
        messages,
        *,
        tools=[],
        tool_choice="auto",
        json_output=None,
        extra_create_args={},
        cancellation_token=None,
    ) -> CreateResult:
        call = FunctionCall(id="call_1", name=self._tool_name, arguments=json.dumps(self._build_arguments()))
        return CreateResult(finish_reason="function_calls", content=[call], usage=self._usage, cached=False)

    async def create_stream(
        self,
        messages,
        *,
        tools=[],
        tool_choice="auto",
        json_output=None,
        extra_create_args={},
        cancellation_token=None,
    ):
        # Never invoked: every AssistantAgent below leaves model_client_stream
        # at its default (False).
        raise NotImplementedError("DeterministicToolCallClient does not stream")
        yield ""  # pragma: no cover - makes this an async generator

    async def close(self) -> None:
        pass

    def actual_usage(self) -> RequestUsage:
        return self._usage

    def total_usage(self) -> RequestUsage:
        return self._usage

    def count_tokens(self, messages, *, tools=[]) -> int:
        return 0

    def remaining_tokens(self, messages, *, tools=[]) -> int:
        return 1_000_000

    @property
    def capabilities(self):
        return {"vision": False, "function_calling": True, "json_output": False}

    @property
    def model_info(self):
        return {
            "vision": False,
            "function_calling": True,
            "json_output": False,
            "family": "unknown",
            "structured_output": False,
        }


def unwrap_tool_result(raw: str) -> dict[str, Any]:
    """Unwrap a FunctionExecutionResult.content string into the workflow_*
    callback tool's real JSON body.

    autogen-ext's McpToolAdapter.run() returns the MCP CallToolResult's
    content blocks (`[{"type": "text", "text": ...}]`), and
    FunctionExecutionResult.content is that structure's json.dumps'd string
    form — the workflow_* tools' text bodies are themselves JSON
    (self_tools_workflow.go's jsonToolResult), so one more json.loads
    recovers the real {output, is_error} / {text, tool_calls, ...} /
    {passed, reason, ...} shape a step routes on. Mirrors
    langgraph_runner.py's parse_tool_output for the equivalent LangChain
    tool-call return shape.
    """
    blocks = json.loads(raw)
    if isinstance(blocks, list):
        text = "".join(block.get("text", "") if isinstance(block, dict) else str(block) for block in blocks)
    else:
        text = raw
    return json.loads(text)


async def run_callback_step(tool_name: str, adapter: Any, build_arguments: Callable[[], dict[str, Any]]) -> dict[str, Any]:
    """Runs one AssistantAgent pinned to exactly one MCP callback tool and
    returns that tool's literal, parsed JSON result.

    The agent's system_message is left unset (None): the deterministic
    model_client never reads it — the loop is scripted, not reasoned about
    — so a prompt would be pure noise.
    """
    agent = AssistantAgent(
        name=f"{tool_name}_runner",
        model_client=DeterministicToolCallClient(tool_name, build_arguments),
        tools=[adapter],
        system_message=None,
    )
    result = await agent.run(task=f"Call {tool_name}.")
    summary = result.messages[-1]
    if not isinstance(summary, ToolCallSummaryMessage) or not summary.results:
        raise RuntimeError(f"{tool_name}: agent run produced no tool call result: {result.messages}")
    exec_result = summary.results[0]
    parsed = unwrap_tool_result(exec_result.content)
    if "is_error" not in parsed:
        parsed["is_error"] = exec_result.is_error
    return parsed


async def main() -> int:
    if len(sys.argv) < 2:
        print("usage: autogen_runner.py <input.json path>", file=sys.stderr)
        return 2

    input_path = sys.argv[1]
    work_dir = os.path.dirname(os.path.abspath(input_path))

    with open(input_path) as f:
        workflow_input = json.load(f)
    with open(os.path.join(work_dir, ".mcp.json")) as f:
        mcp_config = json.load(f)

    server = mcp_config["mcpServers"]["nanite"]
    server_params = StdioServerParams(
        command=server["command"],
        args=server["args"],
        env={**os.environ, **(server.get("env") or {})},
    )

    print(f"workflow input: {workflow_input}")

    # autogen-ext's native discovery path: connects to the .mcp.json-planted
    # `nanite mcp` subprocess and returns one Tool adapter per advertised
    # tool, exactly like a real AutoGen integration would use it.
    adapters = await mcp_server_tools(server_params)
    adapters_by_name = {adapter.name: adapter for adapter in adapters}
    required = [
        "workflow_execute_tool_step",
        "workflow_execute_llm_step",
        "workflow_verify_step",
    ]
    missing = [name for name in required if name not in adapters_by_name]
    if missing:
        print(f"missing required MCP tools: {missing}", file=sys.stderr)
        return 1

    # StepKindTool: engine-owned call, the literal result feeds forward —
    # an LLM never sees or chooses this call (design doc).
    tool_result = await run_callback_step(
        "workflow_execute_tool_step",
        adapters_by_name["workflow_execute_tool_step"],
        lambda: {"tool": "tool_list", "args": {}},
    )

    # StepKindLLM: a capability-restricted agent turn. tools=[] on the
    # callback's own args here — this step does no tool-calling of its own,
    # it just proves an LLM turn can be driven through the harness from an
    # AutoGen agent.
    provider = os.environ.get("NANITE_TEST_PROVIDER", "anthropic")
    model = os.environ.get("NANITE_TEST_MODEL", "claude-sonnet-5")
    llm_result = await run_callback_step(
        "workflow_execute_llm_step",
        adapters_by_name["workflow_execute_llm_step"],
        lambda: {
            "provider": provider,
            "model": model,
            "messages": [{"role": "user", "content": "Reply with the single word: ok"}],
        },
    )

    # verify modifier (mode=engine) over the tool step's literal output —
    # deterministic, no LLM involved (design doc: "verify is a modifier on
    # llm/tool steps, not a fourth step kind").
    verify_result = await run_callback_step(
        "workflow_verify_step",
        adapters_by_name["workflow_verify_step"],
        lambda: {
            "mode": "engine",
            "engine_check": "no_error",
            "subject": {
                "step_kind": "tool",
                "output": json.dumps(tool_result),
                "is_error": bool(tool_result.get("is_error", False)),
            },
        },
    )

    final_state = {
        "tool_result": tool_result,
        "llm_result": llm_result,
        "verify_result": verify_result,
    }
    print(json.dumps(final_state, indent=2, default=str))

    tool_ok = not tool_result.get("is_error", True)
    # A structured llm_result (even a provider/auth error) proves the round
    # trip — see module docstring and smoke_test.py's identical scoping note.
    llm_ok = bool(llm_result)
    verify_ok = bool(verify_result.get("passed", False))

    checks = {"tool_step": tool_ok, "llm_step": llm_ok, "verify_step": verify_ok}
    print(f"checks: {checks}")
    return 0 if all(checks.values()) else 1


if __name__ == "__main__":
    sys.exit(asyncio.run(main()))
