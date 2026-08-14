#!/usr/bin/env python3
"""CrewAI production runner for Nanite's Agent Workflows "crewai" engine
(CW-20260814-0003). Originally authored as the CrewAI POC validating the
MCP callback mechanism (CW-20260813-0013) and promoted here unchanged — a
WorkflowDefinition with Engine: "crewai" (agentworkflow.EngineCrewAI) runs
exactly this hand-authored crew; there is still no compiler from Nanite's
own workflow-definition format to CrewAI's (design doc, "POC scope" —
that stays a non-goal). Embedded into the nanite binary via
internal/workflowrunner's go:embed and materialized to disk at startup
(internal/workflowrunner.MaterializeScripts) so a Cerberus-deployed binary
— which ships alone, with no guarantee a repo checkout sits alongside it —
can still locate it.

A small, hand-authored CrewAI crew — built directly with CrewAI's own
Agent/Task/Process authoring API, not compiled from any Nanite-side workflow
schema (design doc, "POC scope"). Every agent's real work reaches Nanite's
callback tools (workflow_execute_tool_step, workflow_execute_llm_step,
workflow_verify_step) through crewai-tools' MCP integration
(`crewai_tools.MCPServerAdapter`), never through CrewAI's own model-calling
machinery. This is a POC: small and demonstrative, not production-shaped.

Why a custom LLM (DeterministicToolCallLLM) instead of a real
provider-backed one: every CrewAI Agent needs *some* `llm` to drive its
ReAct loop — that's intrinsic to the Agent/Task/Process model, not
something this POC can avoid while still "using CrewAI's own native
authoring API". But handing that orchestration LLM real provider
credentials would mean CrewAI's own agents making real model calls
directly against a provider, completely bypassing Nanite — exactly the
"second, parallel harness" the design doc's one principle forbids
("Workflow engines only decide sequencing... every unit of work... always
executes through Nanite's existing harness, never through the engine
itself"). DeterministicToolCallLLM has no provider, no credentials, no
network call of its own: it deterministically emits the ReAct
Action/Action Input text needed to invoke exactly one Nanite callback tool,
then relays that tool's literal result back as the Final Answer. CrewAI's
own orchestration layer stays a pure, local sequencer; the crewai-tools MCP
integration is what actually reaches Nanite's harness for every unit of
real work — LLM turns included.

Crew shape (three agents/tasks, one per step kind, matching the LangGraph
POC's shape for parity):

    tool_task -> llm_task -> verify_task   (Process.sequential)

  - tool_task  (StepKindTool): workflow_execute_tool_step, engine-owned,
    never touches an LLM.
  - llm_task   (StepKindLLM): workflow_execute_llm_step, a capability-
    restricted agent turn — the one REAL model call in this run, made by
    Nanite's harness, not by CrewAI.
  - verify_task (the `verify` modifier, mode=engine): workflow_verify_step
    checking the tool task's literal output — deterministic, no LLM.

Usage:    python3 crewai_runner.py <input.json path>
Requires: pip install -r requirements.txt (Python 3.10-3.13; crewai's
          numpy/chromadb dependency chain has no prebuilt wheels for very
          new interpreters yet)

Mirrors smoke_test.py's environment overrides and its scoping note on
workflow_execute_llm_step: a provider without real credentials configured
on the target Nanite instance still returns a structured tool result (not
a crashed run), which is enough to prove the callback round trip (CrewAI
task -> crewai-tools MCP adapter -> MCP -> self-tool -> StepExecutor).
"""
import json
import os
import sys
from collections.abc import Callable
from typing import Any

from crewai import Agent, BaseLLM, Crew, Process, Task
from crewai_tools import MCPServerAdapter
from mcp import StdioServerParameters


class DeterministicToolCallLLM(BaseLLM):
    """A local, credential-free stand-in for an Agent's `llm`.

    Drives exactly one ReAct turn: on its first call() it emits the
    Action/Action Input text CrewAI's text-tooling parser
    (crewai.agents.parser) expects to invoke `tool_name`; on the following
    call() — once CrewAI has executed that tool for real (through
    crewai-tools' MCP adapter) and appended "Observation: <result>" to the
    conversation — it relays that literal result back as the Final Answer.
    See the module docstring for why this exists instead of a real
    provider-backed LLM.
    """

    tool_name: str
    build_action_input: Callable[[], dict[str, Any]]
    on_result: Callable[[str], None] | None = None

    def call(
        self,
        messages,
        tools=None,
        callbacks=None,
        available_functions=None,
        from_task=None,
        from_agent=None,
        response_model=None,
    ) -> str:
        # CrewAI's ReAct system/user prompt embeds a few-shot template that
        # itself contains the literal word "Observation:" as instructional
        # boilerplate ("Observation: the result of the action") — a bare
        # substring search for "Observation:" matches that template on the
        # very first call, before the tool ever ran. Requiring our own
        # "Action: <tool_name>" marker alongside it disambiguates: only a
        # message we ourselves emitted, now with a REAL observation
        # appended by CrewAI's tool executor, carries both. CrewAI also
        # appends a trailing nudge message ("Analyze the tool result...")
        # after the Observation before calling us again, so it's not
        # necessarily the *last* message either — scan backward for it.
        observation = None
        marker = f"Action: {self.tool_name}"
        for msg in reversed(messages):
            content = msg.get("content", "") if isinstance(msg, dict) else str(msg)
            if marker in content and "\nObservation:" in content:
                observation = content.split("\nObservation:", 1)[1].strip()
                break

        if observation is not None:
            if self.on_result:
                self.on_result(observation)
            return f"Thought: {self.tool_name} returned a result.\nFinal Answer: {observation}"

        action_input = json.dumps(self.build_action_input())
        return (
            f"Thought: I must call {self.tool_name} to do the real work.\n"
            f"Action: {self.tool_name}\n"
            f"Action Input: {action_input}"
        )


def build_crew(tools_by_name: dict[str, Any]):
    tool_result_holder: dict[str, str] = {}

    provider = os.environ.get("NANITE_TEST_PROVIDER", "anthropic")
    model = os.environ.get("NANITE_TEST_MODEL", "claude-sonnet-5")

    tool_agent = Agent(
        role="Tool Step Runner",
        goal="Invoke workflow_execute_tool_step and report its literal result.",
        backstory="Executes engine-owned tool steps. Never reasons about content, only relays results.",
        tools=[tools_by_name["workflow_execute_tool_step"]],
        llm=DeterministicToolCallLLM(
            model="deterministic-stub",
            tool_name="workflow_execute_tool_step",
            build_action_input=lambda: {"tool": "tool_list", "args": {}},
            on_result=lambda raw: tool_result_holder.update(raw=raw),
        ),
        verbose=False,
    )
    tool_task = Task(
        description="Call workflow_execute_tool_step to list Nanite's registered self-tools.",
        expected_output="The literal {output, is_error} JSON result of the tool call.",
        agent=tool_agent,
    )

    llm_agent = Agent(
        role="LLM Step Runner",
        goal="Invoke workflow_execute_llm_step and report its literal result.",
        backstory="Executes capability-restricted LLM steps through Nanite's harness. Never calls a model itself.",
        tools=[tools_by_name["workflow_execute_llm_step"]],
        llm=DeterministicToolCallLLM(
            model="deterministic-stub",
            tool_name="workflow_execute_llm_step",
            build_action_input=lambda: {
                "provider": provider,
                "model": model,
                "messages": [
                    {"role": "user", "content": "Reply with the single word: ok"}
                ],
            },
        ),
        verbose=False,
    )
    llm_task = Task(
        description="Call workflow_execute_llm_step to run one real agent turn through Nanite's harness.",
        expected_output="The literal {text, tool_calls, usage, stop_reason} JSON result of the LLM step.",
        agent=llm_agent,
        context=[tool_task],
    )

    def verify_action_input() -> dict[str, Any]:
        # Reads tool_result_holder, populated by tool_agent's LLM once
        # tool_task's Observation comes back — Process.sequential
        # guarantees tool_task has fully completed before verify_task
        # starts, so this is always populated by the time it's called.
        parsed = json.loads(tool_result_holder.get("raw", "{}"))
        return {
            "mode": "engine",
            "engine_check": "no_error",
            "subject": {
                "step_kind": "tool",
                "output": json.dumps(parsed),
                "is_error": bool(parsed.get("is_error", False)),
            },
        }

    verify_agent = Agent(
        role="Verify Step Runner",
        goal="Invoke workflow_verify_step over the tool step's result and report the verdict.",
        backstory="Checks a prior step's literal output — never trusts a step's self-report.",
        tools=[tools_by_name["workflow_verify_step"]],
        llm=DeterministicToolCallLLM(
            model="deterministic-stub",
            tool_name="workflow_verify_step",
            build_action_input=verify_action_input,
        ),
        verbose=False,
    )
    verify_task = Task(
        description="Call workflow_verify_step (mode=engine, engine_check=no_error) over the tool step's result.",
        expected_output="The literal {passed, reason} JSON result of the verify call.",
        agent=verify_agent,
        context=[tool_task],
    )

    crew = Crew(
        agents=[tool_agent, llm_agent, verify_agent],
        tasks=[tool_task, llm_task, verify_task],
        process=Process.sequential,
        verbose=False,
    )
    return crew, tool_task, llm_task, verify_task


def main() -> int:
    if len(sys.argv) < 2:
        print("usage: crewai_runner.py <input.json path>", file=sys.stderr)
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

    required = [
        "workflow_execute_tool_step",
        "workflow_execute_llm_step",
        "workflow_verify_step",
    ]
    with MCPServerAdapter(params, *required) as mcp_tools:
        tools_by_name = {tool.name: tool for tool in mcp_tools}
        missing = [name for name in required if name not in tools_by_name]
        if missing:
            print(f"missing required MCP tools: {missing}", file=sys.stderr)
            return 1

        crew, tool_task, llm_task, verify_task = build_crew(tools_by_name)
        crew.kickoff(inputs=workflow_input.get("params", {}))

    tool_result = json.loads(tool_task.output.raw)
    llm_result_text = llm_task.output.raw
    verify_result = json.loads(verify_task.output.raw)

    print(
        json.dumps(
            {
                "tool_result": tool_result,
                "llm_result_text": llm_result_text,
                "verify_result": verify_result,
            },
            indent=2,
        )
    )

    tool_ok = not tool_result.get("is_error", True)
    # A non-empty relayed result (even a provider/auth error) proves the
    # round trip — see module docstring and smoke_test.py's identical
    # scoping note.
    llm_ok = bool(llm_result_text)
    verify_ok = bool(verify_result.get("passed", False))

    checks = {"tool_step": tool_ok, "llm_step": llm_ok, "verify_step": verify_ok}
    print(f"checks: {checks}")
    return 0 if all(checks.values()) else 1


if __name__ == "__main__":
    sys.exit(main())
