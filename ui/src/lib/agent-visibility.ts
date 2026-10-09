/** Shared chat-selection rule. Management lists and existing rosters retain disabled
 * profiles for inspection; this presentation rule conveys no execution authority. */
export function isSelectableAgent(agent: { status?: string }): boolean {
  return agent.status !== "disabled";
}
