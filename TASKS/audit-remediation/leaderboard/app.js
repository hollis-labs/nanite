import {
  SEVERITY_ORDER,
  filterAndSort,
  normalizeCatalog,
  priorityScore,
  severityCounts,
  statusProgress,
  summarize,
  uniqueValues,
} from "./leaderboard.js";

const POLL_INTERVAL_MS = 5_000;
const params = new URLSearchParams(window.location.search);
const dataUrl = params.get("data") || "../findings.json";

const state = {
  catalog: null,
  findings: [],
  dataUrl,
  fileMode: false,
  timer: null,
  fingerprints: new Map(),
};

const elements = {
  sourceName: document.querySelector("#source-name"),
  sourceStatus: document.querySelector("#source-status"),
  refreshButton: document.querySelector("#refresh-button"),
  progressValue: document.querySelector("#progress-value"),
  progressTrack: document.querySelector("#progress-track"),
  progressFill: document.querySelector("#progress-fill"),
  openValue: document.querySelector("#open-value"),
  totalNote: document.querySelector("#total-note"),
  urgentValue: document.querySelector("#urgent-value"),
  decisionValue: document.querySelector("#decision-value"),
  auditCoordinate: document.querySelector("#audit-coordinate"),
  auditMeta: document.querySelector("#audit-meta"),
  severityLanes: document.querySelector("#severity-lanes"),
  laneTemplate: document.querySelector("#lane-template"),
  resultCount: document.querySelector("#result-count"),
  findingRows: document.querySelector("#finding-rows"),
  emptyState: document.querySelector("#empty-state"),
  errorBanner: document.querySelector("#error-banner"),
  searchInput: document.querySelector("#search-input"),
  severityFilter: document.querySelector("#severity-filter"),
  statusFilter: document.querySelector("#status-filter"),
  dispositionFilter: document.querySelector("#disposition-filter"),
  categoryFilter: document.querySelector("#category-filter"),
  sortSelect: document.querySelector("#sort-select"),
  clearButton: document.querySelector("#clear-button"),
  emptyClearButton: document.querySelector("#empty-clear-button"),
  fileInput: document.querySelector("#file-input"),
  detailDialog: document.querySelector("#detail-dialog"),
  detailRank: document.querySelector("#detail-rank"),
  detailContent: document.querySelector("#detail-content"),
  dialogClose: document.querySelector("#dialog-close"),
};

function node(tag, className, text) {
  const element = document.createElement(tag);
  if (className) element.className = className;
  if (text !== undefined) element.textContent = text;
  return element;
}

function titleCase(value) {
  return String(value)
    .split("-")
    .map((part) => part.charAt(0).toUpperCase() + part.slice(1))
    .join(" ");
}

function compactPath(value) {
  if (value.length <= 42) return value;
  return `…/${value.split("/").slice(-3).join("/")}`;
}

function filters() {
  return {
    query: elements.searchInput.value,
    severity: elements.severityFilter.value,
    status: elements.statusFilter.value,
    disposition: elements.dispositionFilter.value,
    category: elements.categoryFilter.value,
    sort: elements.sortSelect.value,
  };
}

function setOptions(select, values, label) {
  const current = select.value;
  select.replaceChildren(new Option(`All ${label}`, ""));
  values.forEach((value) => select.add(new Option(titleCase(value), value)));
  select.value = values.includes(current) ? current : "";
}

function populateFilters() {
  setOptions(elements.severityFilter, SEVERITY_ORDER.filter((value) => state.findings.some((item) => item.severity === value)), "severities");
  setOptions(elements.statusFilter, uniqueValues(state.findings, "task_status"), "statuses");
  setOptions(elements.dispositionFilter, uniqueValues(state.findings, "disposition"), "dispositions");
  setOptions(elements.categoryFilter, uniqueValues(state.findings, "category"), "categories");
}

function renderSummary() {
  const summary = summarize(state.findings);
  elements.progressValue.textContent = summary.progress;
  elements.progressTrack.setAttribute("aria-valuenow", String(summary.progress));
  elements.progressFill.style.width = `${summary.progress}%`;
  elements.openValue.textContent = summary.open;
  elements.totalNote.textContent = `of ${summary.total} cataloged`;
  elements.urgentValue.textContent = summary.urgent;
  elements.decisionValue.textContent = summary.decisions;
  elements.auditCoordinate.textContent = state.catalog.commit || "No commit";
  elements.auditMeta.textContent = state.catalog.audit_date
    ? `Audit ${state.catalog.audit_date}`
    : "Audit date unavailable";
}

function renderSeverityLanes() {
  elements.severityLanes.replaceChildren();
  const counts = severityCounts(state.findings);
  const max = Math.max(...counts.map((item) => item.total), 1);

  counts.forEach(({ severity, open, total }) => {
    const fragment = elements.laneTemplate.content.cloneNode(true);
    const lane = fragment.querySelector(".lane");
    lane.dataset.severity = severity;
    fragment.querySelector(".lane__name").textContent = titleCase(severity);
    fragment.querySelector(".lane__count").textContent = `${open} / ${total}`;
    fragment.querySelector(".lane__track span").style.width = `${(open / max) * 100}%`;
    elements.severityLanes.append(fragment);
  });
}

function badge(value, kind = "neutral") {
  const element = node("span", `badge badge--${kind}`, titleCase(value));
  element.dataset.value = value;
  return element;
}

function renderRows() {
  const orderedAll = filterAndSort([...state.findings], {
    query: "",
    severity: "",
    status: "",
    disposition: "",
    category: "",
    sort: "priority",
  });
  const rankById = new Map(orderedAll.map((finding, index) => [finding.id, index + 1]));
  const visible = filterAndSort([...state.findings], filters());
  elements.findingRows.replaceChildren();
  elements.resultCount.textContent = `${visible.length} of ${state.findings.length} findings`;
  elements.emptyState.hidden = visible.length > 0;

  visible.forEach((finding) => {
    const row = document.createElement("tr");
    row.dataset.severity = finding.severity;
    if (state.fingerprints.get(finding.id)?.changed) row.classList.add("row--changed");

    const rankCell = node("td", "rank-cell");
    rankCell.append(node("span", "rank-number", String(rankById.get(finding.id))));
    row.append(rankCell);

    const findingCell = node("td", "finding-cell");
    const idButton = node("button", "finding-id", finding.id);
    idButton.type = "button";
    idButton.addEventListener("click", () => openDetail(finding, rankById.get(finding.id)));
    findingCell.append(idButton, node("p", "finding-summary", finding.summary));
    const categoryLine = node("div", "category-line");
    finding.category.slice(0, 3).forEach((category) => categoryLine.append(badge(category)));
    if (finding.category.length > 3) categoryLine.append(node("span", "category-more", `+${finding.category.length - 3}`));
    findingCell.append(categoryLine);
    row.append(findingCell);

    const severityCell = document.createElement("td");
    severityCell.append(badge(finding.severity, "severity"));
    row.append(severityCell);

    const statusCell = document.createElement("td");
    statusCell.append(badge(finding.task_status, "status"));
    const microProgress = node("span", "micro-progress");
    const microFill = node("span");
    microFill.style.width = `${statusProgress(finding.task_status)}%`;
    microProgress.append(microFill);
    statusCell.append(microProgress);
    row.append(statusCell);

    const dispositionCell = document.createElement("td");
    dispositionCell.append(badge(finding.disposition, "disposition"));
    if (finding.requires_architect_decision) dispositionCell.append(node("span", "decision-mark", "AD"));
    row.append(dispositionCell);

    row.append(node("td", "package-cell", finding.package));

    const score = priorityScore(finding);
    const signalCell = node("td", "signal-cell");
    const meter = node("span", "signal-meter");
    const fill = node("span");
    fill.style.width = `${Math.min(100, (score / 625) * 100)}%`;
    meter.append(fill);
    signalCell.append(node("span", "signal-score", String(score)), meter);
    row.append(signalCell);
    elements.findingRows.append(row);
  });
}

function addDetailSection(parent, title, content) {
  if (!content || (Array.isArray(content) && content.length === 0)) return;
  const section = node("section", "detail-section");
  section.append(node("h3", "detail-section__title", title));
  if (Array.isArray(content)) {
    const list = node("ul", "detail-list");
    content.forEach((item) => list.append(node("li", "", item)));
    section.append(list);
  } else {
    section.append(node("p", "detail-copy", content));
  }
  parent.append(section);
}

function openDetail(finding, rank) {
  elements.detailRank.textContent = `Priority rank ${rank} · Signal ${priorityScore(finding)}`;
  elements.detailContent.replaceChildren();
  const heading = node("div", "detail-heading");
  heading.append(node("p", "detail-id", finding.id), node("h2", "", finding.summary));
  const badges = node("div", "detail-badges");
  badges.append(badge(finding.severity, "severity"), badge(finding.task_status, "status"), badge(finding.disposition, "disposition"));
  if (finding.requires_architect_decision) badges.append(node("span", "decision-mark decision-mark--large", "Architect decision"));
  heading.append(badges);
  elements.detailContent.append(heading);

  const facts = node("dl", "detail-facts");
  const factValues = [
    ["Package", finding.package],
    ["Confidence", titleCase(finding.confidence)],
    ["Report", finding.report_section || "—"],
    ["Categories", finding.category.map(titleCase).join(", ") || "—"],
  ];
  factValues.forEach(([term, value]) => {
    facts.append(node("dt", "", term), node("dd", "", value));
  });
  elements.detailContent.append(facts);

  addDetailSection(elements.detailContent, "Recommendation", finding.recommendation);
  addDetailSection(elements.detailContent, "Evidence", finding.evidence);
  addDetailSection(elements.detailContent, "Revalidation note", finding.revalidation_note);
  addDetailSection(elements.detailContent, "False-positive considerations", finding.false_positive_considerations);
  addDetailSection(elements.detailContent, "Files", finding.files.map(compactPath));

  if (finding.task_file) {
    const footer = node("footer", "detail-footer");
    const label = node("span", "", "Task file");
    const link = node("a", "detail-link", compactPath(finding.task_file));
    link.href = `../${finding.task_file.replace(/^TASKS\/audit-remediation\//, "")}`;
    link.target = "_blank";
    footer.append(label, link);
    elements.detailContent.append(footer);
  }

  elements.detailDialog.showModal();
  document.body.classList.add("modal-open");
}

function render() {
  renderSummary();
  renderSeverityLanes();
  renderRows();
}

function fingerprint(finding) {
  return JSON.stringify([finding.severity, finding.task_status, finding.disposition, finding.requires_architect_decision, finding.summary]);
}

function recordChanges(findings) {
  const next = new Map();
  findings.forEach((finding) => {
    const value = fingerprint(finding);
    const previous = state.fingerprints.get(finding.id)?.value;
    next.set(finding.id, { value, changed: Boolean(previous && previous !== value) });
  });
  state.fingerprints = next;
}

function applyCatalog(raw, sourceLabel) {
  const catalog = normalizeCatalog(raw);
  recordChanges(catalog.findings);
  state.catalog = catalog;
  state.findings = catalog.findings;
  elements.sourceName.textContent = sourceLabel;
  elements.sourceStatus.textContent = state.fileMode
    ? `Loaded ${new Date().toLocaleTimeString()}`
    : `Live · refreshed ${new Date().toLocaleTimeString()}`;
  elements.errorBanner.hidden = true;
  populateFilters();
  render();
}

async function loadRemote({ quiet = false } = {}) {
  if (state.fileMode) return;
  if (!quiet) elements.sourceStatus.textContent = "Refreshing…";
  try {
    const separator = state.dataUrl.includes("?") ? "&" : "?";
    const response = await fetch(`${state.dataUrl}${separator}_=${Date.now()}`, { cache: "no-store" });
    if (!response.ok) throw new Error(`Source returned HTTP ${response.status}.`);
    applyCatalog(await response.json(), state.dataUrl.split("/").pop() || state.dataUrl);
  } catch (error) {
    elements.sourceStatus.textContent = "Source unavailable";
    elements.errorBanner.hidden = false;
    elements.errorBanner.textContent = `${error.message} Serve this directory over HTTP or load a JSON file from your computer.`;
  }
}

function clearFilters() {
  elements.searchInput.value = "";
  elements.severityFilter.value = "";
  elements.statusFilter.value = "";
  elements.dispositionFilter.value = "";
  elements.categoryFilter.value = "";
  elements.sortSelect.value = "priority";
  renderRows();
}

[elements.searchInput, elements.severityFilter, elements.statusFilter, elements.dispositionFilter, elements.categoryFilter, elements.sortSelect]
  .forEach((control) => control.addEventListener("input", renderRows));

elements.clearButton.addEventListener("click", clearFilters);
elements.emptyClearButton.addEventListener("click", clearFilters);
elements.refreshButton.addEventListener("click", () => {
  if (state.fileMode) elements.fileInput.click();
  else loadRemote();
});
elements.fileInput.addEventListener("change", async (event) => {
  const [file] = event.target.files;
  if (!file) return;
  try {
    state.fileMode = true;
    clearInterval(state.timer);
    applyCatalog(JSON.parse(await file.text()), file.name);
    elements.refreshButton.textContent = "Change file";
  } catch (error) {
    elements.errorBanner.hidden = false;
    elements.errorBanner.textContent = `Could not load ${file.name}: ${error.message}`;
  }
});
elements.dialogClose.addEventListener("click", () => elements.detailDialog.close());
elements.detailDialog.addEventListener("close", () => document.body.classList.remove("modal-open"));
elements.detailDialog.addEventListener("click", (event) => {
  if (event.target === elements.detailDialog) elements.detailDialog.close();
});

loadRemote();
state.timer = window.setInterval(() => loadRemote({ quiet: true }), POLL_INTERVAL_MS);
