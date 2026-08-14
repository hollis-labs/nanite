#!/usr/bin/env python3
"""Google ADK production runner for Nanite's Agent Workflows "google_adk"
engine (CW-20260814-0007), extending the external-engine mechanism
(CW-20260813-0011/CW-20260814-0003) past LangGraph/CrewAI. A
WorkflowDefinition with Engine: "google_adk" (agentworkflow.EngineGoogleADK)
runs exactly this hand-authored agent set; there is still no compiler from
Nanite's own workflow-definition format to ADK's (design doc, "POC scope" —
that stays a non-goal). Embedded into the nanite binary via
internal/workflowrunner's go:embed and materialized to disk at startup
(internal/workflowrunner.MaterializeScripts) so a Cerberus-deployed binary
— which ships alone, with no guarantee a repo checkout sits alongside it —
can still locate it.

Three small, hand-authored ADK LlmAgents — built directly with ADK's own
Agent/Runner/MCPToolset authoring API (google.adk.agents, google.adk.runners,
google.adk.tools.mcp_tool.mcp_toolset), not compiled from any Nanite-side
workflow schema. Every agent's real work reaches Nanite's callback tools
(workflow_execute_tool_step, workflow_execute_llm_step, workflow_verify_step)
through ADK's native MCPToolset (stdio transport, the same .mcp.json the
other runners point at), never through ADK's own model-calling machinery.

Why a custom model (DeterministicToolCallLlm) instead of a real
provider-backed one: every ADK LlmAgent needs *some* `model` to drive its own
function-calling loop — that's intrinsic to the Agent model, not something
this runner can avoid while still "using ADK's own native authoring API".
But handing that agent a real provider-backed model would mean ADK's own
model layer making real calls directly against a provider, completely
bypassing Nanite — exactly the "second, parallel harness" the design doc's
one principle forbids ("Workflow engines only decide sequencing... every
unit of work... always executes through Nanite's existing harness, never
through the engine itself"). DeterministicToolCallLlm
(google.adk.models.BaseLlm subclass — the ADK SDK's own test-double pattern,
see google.adk.cli.agent_test_runner.MockModel) has no provider, no
credentials, no network call of its own: it deterministically emits the
FunctionCall part needed to invoke exactly one Nanite callback tool, then —
once ADK's own tool executor has run that call for real through MCPToolset
and appended the resulting FunctionResponse into llm_request.contents —
relays that literal result back as the agent's final text response. ADK's
own LlmAgent/Runner orchestration stays a pure, local sequencer; MCPToolset
is what actually reaches Nanite's harness for every unit of real work — LLM
turns included.

Agent shape (three single-purpose LlmAgents, one per step kind, matching
the LangGraph/CrewAI POCs' shape for parity):

    tool_step -> llm_step -> verify_step   (run sequentially by this script)

  - tool_step  (StepKindTool): workflow_execute_tool_step, engine-owned,
    never touches an LLM.
  - llm_step   (StepKindLLM): workflow_execute_llm_step, a capability-
    restricted agent turn — the one REAL model call in this run, made by
    Nanite's harness, not by ADK.
  - verify_step (the `verify` modifier, mode=engine): workflow_verify_step
    checking the tool step's literal output — deterministic, no LLM.

Usage:    python3 google_adk_runner.py <input.json path>
Requires: pip install -r requirements.txt

Mirrors smoke_test.py's environment overrides and its scoping note on
workflow_execute_llm_step: a provider without real credentials configured on
the target Nanite instance still returns a structured CallToolResult (not a
transport exception), which is enough to prove the callback round trip
(ADK agent -> MCPToolset -> MCP -> self-tool -> StepExecutor) — see the
llm_step agent below.
"""
import asyncio
import json
import os
import sys
from typing import Any, AsyncGenerator, Callable

from google.adk.agents import LlmAgent
from google.adk.models import BaseLlm
from google.adk.models.llm_request import LlmRequest
from google.adk.models.llm_response import LlmResponse
from google.adk.runners import InMemoryRunner
from google.adk.tools.mcp_tool.mcp_toolset import McpToolset
from google.adk.tools.mcp_tool.mcp_session_manager import StdioConnectionParams
from google.genai import types
from mcp import StdioServerParameters


class DeterministicToolCallLlm(BaseLlm):
    """A local, credential-free stand-in for an LlmAgent's `model`.

    Drives exactly one function-calling turn: on its first
    generate_content_async() it yields a FunctionCall part naming
    `tool_name`; on the following call — once ADK's own tool executor has
    run that call for real (through MCPToolset) and appended a
    FunctionResponse for `tool_name` into llm_request.contents — it relays
    that literal result back as the final text response. See the module
    docstring for why this exists instead of a real provider-backed model.
    """

    tool_name: str
    build_args: Callable[[], dict[str, Any]]

    @classmethod
    def supported_models(cls) -> list[str]:
        return ["deterministic-stub"]

    async def generate_content_async(
        self, llm_request: LlmRequest, stream: bool = False
    ) -> AsyncGenerator[LlmResponse, None]:
        relayed = self._find_function_response(llm_request)
        if relayed is not None:
            yield LlmResponse(
                content=types.Content(
                    role="model", parts=[types.Part(text=relayed)]
                )
            )
            return

        yield LlmResponse(
            content=types.Content(
                role="model",
                parts=[
                    types.Part(
                        function_call=types.FunctionCall(
                            name=self.tool_name, args=self.build_args()
                        )
                    )
                ],
            )
        )

    def _find_function_response(self, llm_request: LlmRequest) -> str | None:
        # ADK appends the executed tool's result as a
        # Content(role="user", parts=[Part(function_response=...)]) once its
        # own tool executor has run the FunctionCall this stub emitted
        # (functions.py's _build_function_response_content). Scan backward —
        # mirrors crewai_runner.py's DeterministicToolCallLLM.call() scanning
        # `messages` for the matching "Observation:" — since this is the
        # equivalent ReAct-style relay point for ADK.
        for content in reversed(llm_request.contents):
            for part in content.parts or []:
                fr = part.function_response
                if fr is not None and fr.name == self.tool_name:
                    blocks = (fr.response or {}).get("content", []) or []
                    return "".join(
                        b.get("text", "") for b in blocks if isinstance(b, dict)
                    )
        return None


def parse_tool_output(text: str) -> dict[str, Any]:
    """Parses a relayed callback tool result's JSON text body.

    DeterministicToolCallLlm._find_function_response already unwraps the MCP
    CallToolResult's content blocks down to their literal text — the
    workflow_* tools' text bodies are themselves JSON
    (self_tools_workflow.go's jsonToolResult), so one json.loads recovers
    the real {output, is_error} / {text, tool_calls, ...} / {passed,
    reason, ...} shape a step routes on.
    """
    return json.loads(text)


def build_toolset(mcp_config: dict[str, Any], tool_name: str) -> McpToolset:
    server = mcp_config["mcpServers"]["nanite"]
    return McpToolset(
        connection_params=StdioConnectionParams(
            server_params=StdioServerParameters(
                command=server["command"],
                args=server["args"],
                env={**os.environ, **(server.get("env") or {})},
            ),
        ),
        tool_filter=[tool_name],
    )


async def run_step(
    tool_name: str, build_args: Callable[[], dict[str, Any]], mcp_config: dict[str, Any]
) -> str:
    """Runs one single-tool ADK agent to completion, returning its relayed
    literal tool-call result text (see DeterministicToolCallLlm)."""
    agent = LlmAgent(
        name=f"{tool_name}_runner",
        model=DeterministicToolCallLlm(
            model="deterministic-stub", tool_name=tool_name, build_args=build_args
        ),
        instruction=f"Invoke {tool_name} and report its literal result.",
        tools=[build_toolset(mcp_config, tool_name)],
    )
    runner = InMemoryRunner(agent=agent, app_name="nanite-workflowrunner")
    session = await runner.session_service.create_session(
        app_name="nanite-workflowrunner", user_id="workflowrunner"
    )

    final_text = ""
    async for event in runner.run_async(
        user_id="workflowrunner",
        session_id=session.id,
        new_message=types.Content(
            role="user", parts=[types.Part(text=f"Run {tool_name}.")]
        ),
    ):
        if event.is_final_response() and event.content and event.content.parts:
            for part in event.content.parts:
                if part.text:
                    final_text += part.text
    return final_text


async def main() -> int:
    if len(sys.argv) < 2:
        print("usage: google_adk_runner.py <input.json path>", file=sys.stderr)
        return 2

    input_path = sys.argv[1]
    work_dir = os.path.dirname(os.path.abspath(input_path))

    with open(input_path) as f:
        workflow_input = json.load(f)
    with open(os.path.join(work_dir, ".mcp.json")) as f:
        mcp_config = json.load(f)

    print(f"workflow input: {workflow_input}")

    # tool_step (StepKindTool): engine-owned call, the literal result feeds
    # forward — an LLM never sees or chooses this call (design doc).
    tool_result_text = await run_step(
        "workflow_execute_tool_step", lambda: {"tool": "tool_list", "args": {}}, mcp_config
    )
    tool_result = parse_tool_output(tool_result_text)

    # llm_step (StepKindLLM): a capability-restricted agent turn. tools=[]
    # here — this step does no tool-calling of its own, it just proves an
    # LLM turn can be driven through the harness from an ADK agent.
    provider = os.environ.get("NANITE_TEST_PROVIDER", "anthropic")
    model = os.environ.get("NANITE_TEST_MODEL", "claude-sonnet-5")
    llm_result_text = await run_step(
        "workflow_execute_llm_step",
        lambda: {
            "provider": provider,
            "model": model,
            "messages": [{"role": "user", "content": "Reply with the single word: ok"}],
        },
        mcp_config,
    )
    llm_result = parse_tool_output(llm_result_text)

    # verify modifier (mode=engine) over the tool step's literal output —
    # deterministic, no LLM involved (design doc: "verify is a modifier on
    # llm/tool steps, not a fourth step kind").
    verify_result_text = await run_step(
        "workflow_verify_step",
        lambda: {
            "mode": "engine",
            "engine_check": "no_error",
            "subject": {
                "step_kind": "tool",
                "output": json.dumps(tool_result),
                "is_error": bool(tool_result.get("is_error", False)),
            },
        },
        mcp_config,
    )
    verify_result = parse_tool_output(verify_result_text)

    final_state = {
        "tool_result": tool_result,
        "llm_result": llm_result,
        "verify_result": verify_result,
    }
    print(json.dumps(final_state, indent=2, default=str))

    tool_ok = not tool_result.get("is_error", True)
    # A structured llm_result (even a provider/auth error) proves the round
    # trip — see module docstring and smoke_test.py's identical scoping
    # note.
    llm_ok = bool(llm_result)
    verify_ok = bool(verify_result.get("passed", False))

    checks = {"tool_step": tool_ok, "llm_step": llm_ok, "verify_step": verify_ok}
    print(f"checks: {checks}")
    return 0 if all(checks.values()) else 1


if __name__ == "__main__":
    sys.exit(asyncio.run(main()))
