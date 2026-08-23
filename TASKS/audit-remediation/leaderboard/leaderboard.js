export const SEVERITY_ORDER = ["critical", "high", "medium", "low", "informational"];

export const STATUS_PROGRESS = {
  "not-started": 0,
  "in-progress": 25,
  implemented: 55,
  validated: 100,
  reviewed: 100,
  done: 100,
};

const TERMINAL_STATUSES = new Set(["validated", "reviewed", "done"]);

const SEVERITY_WEIGHT = {
  critical: 500,
  high: 380,
  medium: 250,
  low: 120,
  informational: 40,
};

const DISPOSITION_WEIGHT = {
  "needs-architect-decision": 75,
  "needs-more-evidence": 60,
  remediate: 50,
  "retire-feature": 45,
  defer: 10,
  "accepted-risk": -120,
  "already-resolved": -180,
  superseded: -180,
  "false-positive": -180,
};

const CLOSED_DISPOSITIONS = new Set([
  "accepted-risk",
  "already-resolved",
  "false-positive",
  "superseded",
]);

export function statusProgress(status) {
  return STATUS_PROGRESS[status] ?? 0;
}

export function isOpen(finding) {
  return !TERMINAL_STATUSES.has(finding.task_status) && !CLOSED_DISPOSITIONS.has(finding.disposition);
}

export function findingProgress(finding) {
  return isOpen(finding) ? statusProgress(finding.task_status) : 100;
}

export function priorityScore(finding) {
  if (!isOpen(finding)) return 0;

  const remaining = 1 - statusProgress(finding.task_status) / 100;
  const severity = SEVERITY_WEIGHT[finding.severity] ?? 0;
  const disposition = DISPOSITION_WEIGHT[finding.disposition] ?? 25;
  const decision = finding.requires_architect_decision ? 35 : 0;
  const confidence = finding.confidence === "high" ? 15 : finding.confidence === "medium" ? 7 : 0;

  return Math.max(1, Math.round(severity * (0.35 + remaining * 0.65) + disposition + decision + confidence));
}

export function normalizeCatalog(value) {
  if (!value || typeof value !== "object" || !Array.isArray(value.findings)) {
    throw new Error("Expected an object with a findings array.");
  }

  const findings = value.findings.map((finding, index) => ({
    id: String(finding.id || `FINDING-${index + 1}`),
    severity: String(finding.severity || "informational").toLowerCase(),
    confidence: String(finding.confidence || "unknown").toLowerCase(),
    category: Array.isArray(finding.category) ? finding.category.map(String) : [],
    package: String(finding.package || "unassigned"),
    files: Array.isArray(finding.files) ? finding.files.map(String) : [],
    symbols: Array.isArray(finding.symbols) ? finding.symbols.map(String) : [],
    summary: String(finding.summary || "No summary provided."),
    evidence: Array.isArray(finding.evidence) ? finding.evidence.map(String) : [],
    recommendation: String(finding.recommendation || "No recommendation provided."),
    requires_architect_decision: Boolean(finding.requires_architect_decision),
    report_section: finding.report_section ? String(finding.report_section) : "",
    false_positive_considerations: finding.false_positive_considerations
      ? String(finding.false_positive_considerations)
      : "",
    task_file: finding.task_file ? String(finding.task_file) : "",
    disposition: String(finding.disposition || "needs-more-evidence").toLowerCase(),
    task_status: String(finding.task_status || "not-started").toLowerCase(),
    revalidation_note: finding.revalidation_note ? String(finding.revalidation_note) : "",
    metrics: finding.metrics && typeof finding.metrics === "object" ? finding.metrics : {},
  }));

  return { ...value, findings };
}

export function summarize(findings) {
  const total = findings.length;
  const open = findings.filter(isOpen).length;
  const urgent = findings.filter(
    (finding) => isOpen(finding) && ["critical", "high"].includes(finding.severity),
  ).length;
  const decisions = findings.filter(
    (finding) => isOpen(finding) && finding.requires_architect_decision,
  ).length;
  const progress = total
    ? Math.round(findings.reduce((sum, finding) => sum + findingProgress(finding), 0) / total)
    : 0;

  return { total, open, urgent, decisions, progress };
}

export function severityCounts(findings) {
  return SEVERITY_ORDER.map((severity) => {
    const matching = findings.filter((finding) => finding.severity === severity);
    return {
      severity,
      total: matching.length,
      open: matching.filter(isOpen).length,
    };
  });
}

export function filterAndSort(findings, filters) {
  const query = filters.query.trim().toLowerCase();
  const result = findings.filter((finding) => {
    if (filters.severity && finding.severity !== filters.severity) return false;
    if (filters.status && finding.task_status !== filters.status) return false;
    if (filters.disposition && finding.disposition !== filters.disposition) return false;
    if (filters.category && !finding.category.includes(filters.category)) return false;
    if (!query) return true;

    const haystack = [
      finding.id,
      finding.summary,
      finding.package,
      finding.severity,
      finding.task_status,
      finding.disposition,
      ...finding.category,
      ...finding.files,
    ]
      .join(" ")
      .toLowerCase();
    return haystack.includes(query);
  });

  return result.sort((a, b) => {
    if (filters.sort === "severity") {
      return SEVERITY_ORDER.indexOf(a.severity) - SEVERITY_ORDER.indexOf(b.severity) || a.id.localeCompare(b.id);
    }
    if (filters.sort === "progress") {
      return findingProgress(b) - findingProgress(a) || priorityScore(b) - priorityScore(a);
    }
    if (filters.sort === "id") return a.id.localeCompare(b.id, undefined, { numeric: true });
    return priorityScore(b) - priorityScore(a) || a.id.localeCompare(b.id);
  });
}

export function uniqueValues(findings, key) {
  const values = findings.flatMap((finding) => (Array.isArray(finding[key]) ? finding[key] : [finding[key]]));
  return [...new Set(values.filter(Boolean))].sort((a, b) => String(a).localeCompare(String(b)));
}
