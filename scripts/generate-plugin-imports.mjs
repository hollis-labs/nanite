#!/usr/bin/env node
// generate-plugin-imports.mjs
// Generates ui/src/generated/plugin-envelopes.ts from:
//   - config/envelopes.yaml (core envelope types)
//   - plugins/*/plugin.yaml (plugin envelope types)
//
// Validates the core manifest against config/envelopes.schema.json.
//
// Usage:
//   node scripts/generate-plugin-imports.mjs          # generate
//   node scripts/generate-plugin-imports.mjs --check  # validate only (CI)

import { readFileSync, writeFileSync, readdirSync, existsSync, statSync } from 'node:fs';
import { join, dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const __filename = fileURLToPath(import.meta.url);
const __dirname = dirname(__filename);
const ROOT = resolve(__dirname, '..');
const PLUGINS_DIR = join(ROOT, 'plugins');
const OUTPUT_FILE = join(ROOT, 'ui', 'src', 'generated', 'plugin-envelopes.ts');
const UI_SRC = join(ROOT, 'ui', 'src');
const MANIFEST_PATH = join(ROOT, 'config', 'envelopes.yaml');

const CHECK_MODE = process.argv.includes('--check');

// --- Minimal YAML parser (handles the flat list-of-objects subset we need) ---

function parseYamlList(content, sectionKey) {
  const entries = [];
  const lines = content.split('\n');
  let inSection = false;
  let currentEntry = null;

  for (const line of lines) {
    const trimmed = line.trimEnd();

    // Detect target section
    if (new RegExp(`^${sectionKey}:\\s*$`).test(trimmed)) {
      inSection = true;
      continue;
    }
    // Another top-level key ends the section
    if (/^\S/.test(trimmed) && !trimmed.startsWith('#') && trimmed !== '') {
      if (inSection) {
        inSection = false;
        break;
      }
      continue;
    }

    if (!inSection) continue;
    if (trimmed.trim() === '' || trimmed.trim().startsWith('#')) continue;

    // List item start: "  - key: value"
    const listMatch = trimmed.match(/^\s+-\s+(\w+):\s*(.+)$/);
    if (listMatch) {
      if (currentEntry) entries.push({ ...currentEntry });
      currentEntry = {};
      currentEntry[listMatch[1]] = listMatch[2].trim();
      continue;
    }

    // List item start without value: "  - key:"  (shouldn't happen but handle)
    const listStartOnly = trimmed.match(/^\s+-\s+(\w+):\s*$/);
    if (listStartOnly) {
      if (currentEntry) entries.push({ ...currentEntry });
      currentEntry = {};
      currentEntry[listStartOnly[1]] = '';
      continue;
    }

    // Continuation key: "    key: value"
    const kvMatch = trimmed.match(/^\s+(\w+):\s*(.+)$/);
    if (kvMatch && currentEntry) {
      currentEntry[kvMatch[1]] = kvMatch[2].trim();
    }
  }

  if (currentEntry) entries.push({ ...currentEntry });
  return entries;
}

// --- Schema validation (subset — checks required fields, patterns, constraints) ---

function validateManifest(entries) {
  const errors = [];
  const typePattern = /^[a-z][a-z0-9-]+$/;
  const exportPattern = /^[A-Z][A-Za-z0-9]+$/;
  const seen = new Set();

  if (!entries || entries.length === 0) {
    errors.push('Manifest must have at least one core entry');
    return errors;
  }

  for (let i = 0; i < entries.length; i++) {
    const e = entries[i];
    const prefix = `core[${i}]`;

    if (!e.type) {
      errors.push(`${prefix}: missing required field "type"`);
      continue;
    }
    if (!typePattern.test(e.type)) {
      errors.push(`${prefix}: type "${e.type}" must match ${typePattern}`);
    }
    if (seen.has(e.type)) {
      errors.push(`${prefix}: duplicate type "${e.type}"`);
    }
    seen.add(e.type);

    // component and export are co-required
    if (e.component && !e.export) {
      errors.push(`${prefix} (${e.type}): "component" requires "export"`);
    }
    if (e.export && !e.component) {
      errors.push(`${prefix} (${e.type}): "export" requires "component"`);
    }
    if (e.export && !exportPattern.test(e.export)) {
      errors.push(`${prefix} (${e.type}): export "${e.export}" must match ${exportPattern}`);
    }
  }

  return errors;
}

// --- Plugin YAML parser (reused from original script) ---

function parsePluginEnvelopes(pluginName, content) {
  const entries = [];
  const lines = content.split('\n');
  let inRegisters = false;
  let inEnvelopes = false;
  let currentEntry = null;

  for (const line of lines) {
    const trimmed = line.trimEnd();

    if (/^registers:\s*$/.test(trimmed)) {
      inRegisters = true;
      inEnvelopes = false;
      continue;
    }
    if (/^\S/.test(trimmed) && !trimmed.startsWith('#') && trimmed !== '') {
      if (inRegisters) inRegisters = false;
      inEnvelopes = false;
      continue;
    }
    if (inRegisters && /^\s+envelopes:\s*$/.test(trimmed)) {
      inEnvelopes = true;
      continue;
    }
    if (inRegisters && /^\s{2}\S/.test(trimmed) && !/^\s+envelopes:/.test(trimmed) && !trimmed.trim().startsWith('#') && !trimmed.trim().startsWith('-')) {
      inEnvelopes = false;
      continue;
    }
    if (!inEnvelopes) continue;

    const listMatch = trimmed.match(/^\s+-\s+(\w+):\s*(.+)$/);
    if (listMatch) {
      if (currentEntry && currentEntry.type && currentEntry.component) {
        entries.push({ ...currentEntry });
      }
      currentEntry = {};
      currentEntry[listMatch[1]] = listMatch[2].trim();
      continue;
    }
    const kvMatch = trimmed.match(/^\s+(\w+):\s*(.+)$/);
    if (kvMatch && currentEntry) {
      currentEntry[kvMatch[1]] = kvMatch[2].trim();
    }
  }

  if (currentEntry && currentEntry.type && currentEntry.component) {
    entries.push({ ...currentEntry });
  }

  return entries.map(e => ({
    type: e.type,
    component: e.component,
    exportName: e.export || e.component.split('/').pop(),
    plugin: pluginName,
  }));
}

// --- Code generation ---

function generateLazyImport(type, component, exportName) {
  return `  "${type}": {
    component: lazy(() =>
      import("@/${component}").then((m) => ({
        default: m.${exportName},
      })),
    ),`;
}

function generateOutput(coreEntries, pluginEntries) {
  const coreWithComponents = coreEntries.filter(e => e.component && e.export);
  const coreWithoutComponents = coreEntries.filter(e => !e.component);

  const coreLines = coreWithComponents.map(e => {
    return `${generateLazyImport(e.type, e.component, e.export)}
    source: "core",
  },`;
  });

  const pluginLines = pluginEntries.map(e => {
    return `${generateLazyImport(e.type, e.component, e.exportName)}
    source: "${e.plugin}",
  },`;
  });

  let coreComment = '';
  if (coreWithoutComponents.length > 0) {
    coreComment = `\n  // Backend-only types (no frontend component): ${coreWithoutComponents.map(e => e.type).join(', ')}`;
  }

  return `// AUTO-GENERATED by scripts/generate-plugin-imports.mjs — do not edit manually.
// Core entries from config/envelopes.yaml, plugin entries from plugins/*/plugin.yaml.
// Run \`npm run generate:plugins\` to regenerate.
import { lazy } from "react";
import type { ComponentType } from "react";
import { getDynamicEnvelope } from "@/lib/plugin-loader";

// biome-ignore lint/suspicious/noExplicitAny: plugin envelope components have varied props
type LazyEnvelopeComponent = React.LazyExoticComponent<ComponentType<any>>;

export interface EnvelopeRegistryEntry {
  component: LazyEnvelopeComponent;
  source: string; // "core" | pluginId
}

// --- CORE ENVELOPES (generated from config/envelopes.yaml) ---
const CORE_ENTRIES: Record<string, EnvelopeRegistryEntry> = {${coreComment}
${coreLines.join('\n')}
};

// --- PLUGIN ENVELOPES (generated from plugins/*/plugin.yaml) ---
const PLUGIN_ENTRIES: Record<string, EnvelopeRegistryEntry> = {
${pluginLines.join('\n')}
};

// Single merged registry — core takes precedence on name collision.
export const ENVELOPE_REGISTRY: Record<string, EnvelopeRegistryEntry> = {
  ...PLUGIN_ENTRIES,
  ...CORE_ENTRIES,
};

/**
 * Get the envelope component for a given type.
 * In recover mode, pass \`recoverMode: true\` to restrict to core-only entries.
 */
export function getEnvelopeComponent(
  type: string,
  recoverMode = false,
): LazyEnvelopeComponent | undefined {
  const entry = ENVELOPE_REGISTRY[type];
  if (entry) {
    if (recoverMode && entry.source !== "core") return undefined;
    return entry.component;
  }

  // Fallback: check dynamically loaded plugins (skip in recover mode).
  if (recoverMode) return undefined;
  const dynamic = getDynamicEnvelope(type);
  return dynamic?.component;
}

// Legacy exports — kept for backward compat with EnvelopeRenderer.
// biome-ignore lint/suspicious/noExplicitAny: legacy export shape
export const PLUGIN_ENVELOPE_REGISTRY: Record<string, React.LazyExoticComponent<ComponentType<any>>> =
  Object.fromEntries(
    Object.entries(ENVELOPE_REGISTRY).map(([k, v]) => [k, v.component]),
  );

// biome-ignore lint/suspicious/noExplicitAny: legacy export shape
export const CORE_ONLY_ENVELOPE_REGISTRY: Record<string, React.LazyExoticComponent<ComponentType<any>>> =
  Object.fromEntries(
    Object.entries(ENVELOPE_REGISTRY)
      .filter(([, v]) => v.source === "core")
      .map(([k, v]) => [k, v.component]),
  );
`;
}

// --- Main ---

function main() {
  console.log('Envelope codegen: generating plugin-envelopes.ts');

  // 1. Load and validate core manifest
  if (!existsSync(MANIFEST_PATH)) {
    console.error(`ERROR: Core envelope manifest not found: ${MANIFEST_PATH}`);
    process.exit(1);
  }

  const manifestContent = readFileSync(MANIFEST_PATH, 'utf-8');
  const coreEntries = parseYamlList(manifestContent, 'core');

  const validationErrors = validateManifest(coreEntries);
  if (validationErrors.length > 0) {
    console.error('ERROR: Envelope manifest validation failed:');
    for (const err of validationErrors) {
      console.error(`  - ${err}`);
    }
    process.exit(1);
  }

  // Verify component files exist for entries that declare them
  for (const e of coreEntries) {
    if (e.component) {
      const componentPath = join(UI_SRC, e.component + '.tsx');
      if (!existsSync(componentPath)) {
        console.error(`ERROR: Core envelope "${e.type}" — component not found: ${componentPath}`);
        process.exit(1);
      }
    }
  }

  console.log(`  Core: ${coreEntries.length} type(s) from config/envelopes.yaml`);

  // 2. Load plugin envelopes
  const pluginEntries = [];
  if (existsSync(PLUGINS_DIR)) {
    const pluginDirs = readdirSync(PLUGINS_DIR).filter(name => {
      const full = join(PLUGINS_DIR, name);
      return statSync(full).isDirectory();
    });

    for (const pluginName of pluginDirs) {
      const yamlPath = join(PLUGINS_DIR, pluginName, 'plugin.yaml');
      if (!existsSync(yamlPath)) continue;

      const content = readFileSync(yamlPath, 'utf-8');
      const envelopes = parsePluginEnvelopes(pluginName, content);

      for (const env of envelopes) {
        const componentPath = join(UI_SRC, env.component + '.tsx');
        if (!existsSync(componentPath)) {
          console.warn(`  SKIP ${env.type} — component not found: ${componentPath}`);
          continue;
        }
        pluginEntries.push(env);
      }
    }
  }

  console.log(`  Plugins: ${pluginEntries.length} type(s) from ${new Set(pluginEntries.map(e => e.plugin)).size} plugin(s)`);

  // 3. Check mode — validate only, don't write
  if (CHECK_MODE) {
    const output = generateOutput(coreEntries, pluginEntries);
    if (existsSync(OUTPUT_FILE)) {
      const existing = readFileSync(OUTPUT_FILE, 'utf-8');
      if (existing === output) {
        console.log('  CHECK PASSED: generated file is up to date');
        process.exit(0);
      } else {
        console.error('  CHECK FAILED: generated file is out of date — run npm run generate:plugins');
        process.exit(1);
      }
    } else {
      console.error('  CHECK FAILED: generated file does not exist');
      process.exit(1);
    }
  }

  // 4. Generate and write
  const output = generateOutput(coreEntries, pluginEntries);
  writeFileSync(OUTPUT_FILE, output, 'utf-8');
  console.log(`  Written: ${OUTPUT_FILE}`);

  // Summary
  for (const e of coreEntries) {
    const status = e.component ? `-> ${e.component}` : '(backend-only)';
    console.log(`    [core] ${e.type} ${status}`);
  }
  for (const e of pluginEntries) {
    console.log(`    [${e.plugin}] ${e.type} -> ${e.component}`);
  }
}

main();
