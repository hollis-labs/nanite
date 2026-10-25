import { expect, it } from "vitest";
import { isSelectableAgent } from "@/lib/agent-visibility";

it("hides disabled and unsupported agents from chat selection, keeps the rest", () => {
  expect(isSelectableAgent({ status: "active" })).toBe(true);
  expect(isSelectableAgent({})).toBe(true);
  expect(isSelectableAgent({ status: "disabled" })).toBe(false);
  expect(isSelectableAgent({ status: "unsupported" })).toBe(false);
});
