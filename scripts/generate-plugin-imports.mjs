#!/usr/bin/env node
// generate-plugin-imports.mjs
// Generates ui/src/generated/plugin-envelopes.ts from config/envelopes.yaml.
//
// After Phase 2 Track D.3, all compiled-in envelopes are core. Runtime plugin
// envelopes are registered dynamically via the plugin registry (see
// ui/src/lib/plugin-loader.ts) — they do not participate in codegen.
//
// Usage:
//   node scripts/generate-plugin-imports.mjs          # generate
//   node scripts/generate-plugin-imports.mjs --check  # validate only (CI)

import { readFileSync, writeFileSync, existsSync } from 'node:fs';
import { join, dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const __filename = fileURLToPath(import.meta.url);
const __dirname = dirname(__filename);
const ROOT = resolve(__dirname, '..');
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

    if (new RegExp(`^${sectionKey}:\\s*$`).test(trimmed)) {
      inSection = true;
      continue;
    }
    if (/^\S/.test(trimmed) && !trimmed.startsWith('#') && trimmed !== '') {
      if (inSection) {
        break;
      }
      continue;
    }

    if (!inSection) continue;
    if (trimmed.trim() === '' || trimmed.trim().startsWith('#')) continue;

    const listMatch = trimmed.match(/^\s+-\s+(\w+):\s*(.+)$/);
    if (listMatch) {
      if (currentEntry) entries.push({ ...currentEntry });
      currentEntry = {};
      currentEntry[listMatch[1]] = listMatch[2].trim();
      continue;
    }

    const listStartOnly = trimmed.match(/^\s+-\s+(\w+):\s*$/);
    if (listStartOnly) {
      if (currentEntry) entries.push({ ...currentEntry });
      currentEntry = {};
      currentEntry[listStartOnly[1]] = '';
      continue;
    }

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

    if (e.component && !e.export) {
      errors.push(`${prefix} (${e.type}): "component" requires "export"`);
    }
    if (e.export && !e.component) {
      errors.push(`${prefix} (${e.type}): "export" requires "component"`);
    }
    if (e.export && !exportPattern.test(e.export)) {
      errors.push(`${prefix} (${e.type}): export "${e.export}" must match ${exportPattern}`);
    }
    if (e.props !== undefined && !['approval', 'proposal', 'envelope'].includes(e.props)) {
      errors.push(`${prefix} (${e.type}): props "${e.props}" must be one of "approval" | "proposal" | "envelope"`);
    }
  }

  return errors;
}

// --- Code generation ---

const VALID_PROPS = new Set(['approval', 'proposal', 'envelope']);

function generateLazyImport(type, component, exportName) {
  return `  "${type}": {
    component: lazy(() =>
      import("@/${component}").then((m) => ({
        default: m.${exportName},
      })),
    ),`;
}

function generateOutput(coreEntries) {
  const coreWithComponents = coreEntries.filter(e => e.component && e.export);
  const coreWithoutComponents = coreEntries.filter(e => !e.component);

  const coreLines = coreWithComponents.map(e => {
    let propsLine = '';
    if (e.props) {
      if (!VALID_PROPS.has(e.props)) {
        throw new Error(`envelope ${e.type}: props "${e.props}" must be one of ${[...VALID_PROPS].join(', ')}`);
      }
      propsLine = `\n    props: "${e.props}",`;
    }
    return `${generateLazyImport(e.type, e.component, e.export)}
    source: "core",${propsLine}
  },`;
  });

  let coreComment = '';
  if (coreWithoutComponents.length > 0) {
    coreComment = `\n  // Backend-only types (no frontend component): ${coreWithoutComponents.map(e => e.type).join(', ')}`;
  }

  return `// AUTO-GENERATED by scripts/generate-plugin-imports.mjs — do not edit manually.
// Core entries from config/envelopes.yaml. Runtime plugin envelopes are
// registered dynamically via plugin-loader.ts and resolved by getDynamicEnvelope.
// Run \`npm run generate:plugins\` to regenerate.
import { lazy } from "react";
import type { ComponentType } from "react";
import { getDynamicEnvelope } from "@/lib/plugin-loader";

// biome-ignore lint/suspicious/noExplicitAny: plugin envelope components have varied props
type LazyEnvelopeComponent = React.LazyExoticComponent<ComponentType<any>>;

export interface EnvelopeRegistryEntry {
  component: LazyEnvelopeComponent;
  source: string; // "core" | pluginId
  /** Prop-shape discriminator used by EnvelopeRenderer to build componentProps.
   * "approval" → { approval }, "proposal" → { proposal },
   * "envelope" → { envelope }, undefined → { data } (default). */
  props?: "approval" | "proposal" | "envelope";
}

// --- CORE ENVELOPES (generated from config/envelopes.yaml) ---
const CORE_ENTRIES: Record<string, EnvelopeRegistryEntry> = {${coreComment}
${coreLines.join('\n')}
};

// Compiled-in registry — core only. Runtime plugins resolve via getDynamicEnvelope.
export const ENVELOPE_REGISTRY: Record<string, EnvelopeRegistryEntry> = {
  ...CORE_ENTRIES,
};

/**
 * Get the registry entry for a given envelope type, or undefined if unregistered.
 * Returns the full EnvelopeRegistryEntry so callers can inspect \`.component\` and
 * \`.props\` (prop-shape discriminator) without a separate lookup.
 * In recover mode, pass \`recoverMode: true\` to restrict to core-only entries.
 */
export function getEnvelopeComponent(
  type: string,
  recoverMode = false,
): EnvelopeRegistryEntry | undefined {
  const entry = ENVELOPE_REGISTRY[type];
  if (entry) {
    if (recoverMode && entry.source !== "core") return undefined;
    return entry;
  }

  // Fallback: check dynamically loaded plugins (skip in recover mode).
  if (recoverMode) return undefined;
  const dynamic = getDynamicEnvelope(type);
  if (!dynamic) return undefined;
  return { component: dynamic.component, source: dynamic.source };
}
`;
}

// --- Main ---

function main() {
  console.log('Envelope codegen: generating plugin-envelopes.ts');

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

  if (CHECK_MODE) {
    const output = generateOutput(coreEntries);
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

  const output = generateOutput(coreEntries);
  writeFileSync(OUTPUT_FILE, output, 'utf-8');
  console.log(`  Written: ${OUTPUT_FILE}`);

  for (const e of coreEntries) {
    const status = e.component ? `-> ${e.component}` : '(backend-only)';
    console.log(`    [core] ${e.type} ${status}`);
  }
}

main();
