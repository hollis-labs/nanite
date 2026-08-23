import assert from "node:assert/strict";
import test from "node:test";

import {
  filterAndSort,
  isOpen,
  normalizeCatalog,
  priorityScore,
  summarize,
} from "./leaderboard.js";

const sample = normalizeCatalog({
  audit_date: "2026-08-21",
  findings: [
    {
      id: "CRIT-1",
      severity: "critical",
      confidence: "high",
      category: ["security"],
      package: "internal/api",
      summary: "Critical open issue",
      disposition: "remediate",
      task_status: "not-started",
      requires_architect_decision: true,
    },
    {
      id: "LOW-1",
      severity: "low",
      category: ["naming"],
      package: "internal/store",
      summary: "Low issue under review",
      disposition: "remediate",
      task_status: "reviewed",
    },
    {
      id: "CLOSED-1",
      severity: "high",
      category: ["security"],
      package: "internal/mcp",
      summary: "Accepted risk",
      disposition: "accepted-risk",
      task_status: "reviewed",
    },
  ],
});

test("normalizes defaults and recognizes closed dispositions", () => {
  assert.equal(sample.findings[0].recommendation, "No recommendation provided.");
  assert.equal(isOpen(sample.findings[2]), false);
});

test("priority favors urgent unresolved work", () => {
  assert.ok(priorityScore(sample.findings[0]) > priorityScore(sample.findings[1]));
  assert.equal(priorityScore(sample.findings[2]), 0);
});

test("summarizes progress and actionable work", () => {
  assert.deepEqual(summarize(sample.findings), {
    total: 3,
    open: 2,
    urgent: 1,
    decisions: 1,
    progress: 60,
  });
});

test("filters across categories and searchable text", () => {
  const visible = filterAndSort([...sample.findings], {
    query: "api",
    severity: "critical",
    status: "",
    disposition: "",
    category: "security",
    sort: "priority",
  });
  assert.deepEqual(visible.map((finding) => finding.id), ["CRIT-1"]);
});
