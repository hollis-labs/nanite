#!/usr/bin/env node
// generate-plugin-imports.mjs
// Scans plugins/*/plugin.yaml for envelope registrations and generates
// a TypeScript import map at ui/src/generated/plugin-envelopes.ts.
//
// IMPORTANT: Core envelope registrations are preserved across regeneration.
// The script only overwrites the PLUGIN_ENTRIES section (between markers).
// Core entries live in CORE_ENVELOPE_REGISTRY and are NEVER touched.
//
// Usage: node scripts/generate-plugin-imports.mjs

import { readFileSync, writeFileSync, readdirSync, existsSync, statSync } from 'node:fs';
import { join, dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const __filename = fileURLToPath(import.meta.url);
const __dirname = dirname(__filename);
const ROOT = resolve(__dirname, '..');
const PLUGINS_DIR = join(ROOT, 'plugins');
const OUTPUT_FILE = join(ROOT, 'ui', 'src', 'generated', 'plugin-envelopes.ts');
const UI_SRC = join(ROOT, 'ui', 'src');

// Minimal YAML parser — only handles the subset we need (registers.envelopes list).
// Each envelope entry has: type, component, export.
function parseEnvelopesFromYaml(content) {
  const envelopes = [];
  const lines = content.split('\n');
  let inRegisters = false;
  let inEnvelopes = false;
  let currentEntry = null;

  for (const line of lines) {
    const trimmed = line.trimEnd();

    // Detect top-level sections
    if (/^registers:\s*$/.test(trimmed)) {
      inRegisters = true;
      inEnvelopes = false;
      continue;
    }
    // Another top-level key ends registers
    if (/^\S/.test(trimmed) && !trimmed.startsWith('#') && trimmed !== '') {
      if (inRegisters) inRegisters = false;
      inEnvelopes = false;
      continue;
    }

    if (inRegisters && /^\s+envelopes:\s*$/.test(trimmed)) {
      inEnvelopes = true;
      continue;
    }
    // Another second-level key under registers ends envelopes
    if (inRegisters && /^\s{2}\S/.test(trimmed) && !/^\s+envelopes:/.test(trimmed) && !trimmed.trim().startsWith('#') && !trimmed.trim().startsWith('-')) {
      inEnvelopes = false;
      continue;
    }

    if (!inEnvelopes) continue;

    // List item start
    const listMatch = trimmed.match(/^\s+-\s+(\w+):\s*(.+)$/);
    if (listMatch) {
      // Save previous entry
      if (currentEntry && currentEntry.type && currentEntry.component) {
        envelopes.push({ ...currentEntry });
      }
      currentEntry = {};
      currentEntry[listMatch[1]] = listMatch[2].trim();
      continue;
    }

    // Continuation key in current list item
    const kvMatch = trimmed.match(/^\s+(\w+):\s*(.+)$/);
    if (kvMatch && currentEntry) {
      currentEntry[kvMatch[1]] = kvMatch[2].trim();
    }
  }

  // Don't forget the last entry
  if (currentEntry && currentEntry.type && currentEntry.component) {
    envelopes.push({ ...currentEntry });
  }

  return envelopes;
}

function main() {
  if (!existsSync(PLUGINS_DIR)) {
    console.warn(`Plugins directory not found: ${PLUGINS_DIR} — generating with empty plugin entries.`);
  }

  const allEnvelopes = [];

  // Scan each plugin directory
  if (existsSync(PLUGINS_DIR)) {
    const pluginDirs = readdirSync(PLUGINS_DIR).filter(name => {
      const full = join(PLUGINS_DIR, name);
      return statSync(full).isDirectory();
    });

    for (const pluginName of pluginDirs) {
      const yamlPath = join(PLUGINS_DIR, pluginName, 'plugin.yaml');
      if (!existsSync(yamlPath)) continue;

      const content = readFileSync(yamlPath, 'utf-8');
      const envelopes = parseEnvelopesFromYaml(content);

      for (const env of envelopes) {
        // Verify the component file actually exists
        const componentPath = join(UI_SRC, env.component + '.tsx');
        if (!existsSync(componentPath)) {
          console.warn(`  SKIP ${env.type} — component not found: ${componentPath}`);
          continue;
        }

        allEnvelopes.push({
          type: env.type,
          component: env.component,
          exportName: env.export || env.component.split('/').pop(),
          plugin: pluginName,
        });
      }
    }
  }

  // Read existing file to preserve core entries.
  // Strategy: replace only the section between @PLUGIN_ENTRIES_START and @PLUGIN_ENTRIES_END.
  const existing = existsSync(OUTPUT_FILE) ? readFileSync(OUTPUT_FILE, 'utf-8') : '';
  const hasMarkers = existing.includes('@PLUGIN_ENTRIES_START') && existing.includes('@PLUGIN_ENTRIES_END');

  if (hasMarkers) {
    // Surgical update: only replace the plugin entries section.
    const pluginImports = allEnvelopes.map(e => {
      return `  '${e.type}': lazy(() => import('@/${e.component}').then(m => ({ default: m.${e.exportName} }))),`;
    });

    const pluginBlock = `// @PLUGIN_ENTRIES_START
const PLUGIN_ENVELOPE_ENTRIES: Record<string, LazyEnvelopeComponent> = {
${pluginImports.join('\n')}
};
// @PLUGIN_ENTRIES_END`;

    const updated = existing.replace(
      /\/\/ @PLUGIN_ENTRIES_START[\s\S]*?\/\/ @PLUGIN_ENTRIES_END/,
      pluginBlock
    );

    writeFileSync(OUTPUT_FILE, updated, 'utf-8');
    console.log(`Updated plugin entries in ${OUTPUT_FILE} (core entries preserved)`);
  } else {
    // No markers — file is in old format. Generate the full new format.
    console.warn('WARNING: plugin-envelopes.ts missing section markers — regenerating with core + plugin structure.');
    console.warn('Core envelope entries may be missing. Check the file and restore manually if needed.');

    const pluginImports = allEnvelopes.map(e => {
      return `  '${e.type}': lazy(() => import('@/${e.component}').then(m => ({ default: m.${e.exportName} }))),`;
    });

    const output = `// AUTO-GENERATED by scripts/generate-plugin-imports.mjs — do not edit manually.
// Run \`npm run generate:plugins\` to regenerate plugin entries.
//
// IMPORTANT: Core envelope registrations (marked CORE below) are NOT generated
// by the script — they are hardcoded here and MUST be preserved across
// regeneration. The generation script only replaces the PLUGIN_ENTRIES section.
import { lazy } from 'react';
import type { ComponentType } from 'react';

// biome-ignore lint/suspicious/noExplicitAny: plugin envelope components have varied props
type LazyEnvelopeComponent = React.LazyExoticComponent<ComponentType<any>>;

// --- CORE ENVELOPES (do not remove — these are NOT plugins) ---
const CORE_ENVELOPE_REGISTRY: Record<string, LazyEnvelopeComponent> = {
  // ADD CORE ENVELOPES HERE — this file was regenerated without them.
  // See git history for the full list.
};

// --- PLUGIN ENTRIES (auto-generated, safe to overwrite below this line) ---
// @PLUGIN_ENTRIES_START
const PLUGIN_ENVELOPE_ENTRIES: Record<string, LazyEnvelopeComponent> = {
${pluginImports.join('\n')}
};
// @PLUGIN_ENTRIES_END

// Merge: core takes precedence over plugins on name collision.
export const PLUGIN_ENVELOPE_REGISTRY: Record<string, LazyEnvelopeComponent> = {
  ...PLUGIN_ENVELOPE_ENTRIES,
  ...CORE_ENVELOPE_REGISTRY,
};
`;

    writeFileSync(OUTPUT_FILE, output, 'utf-8');
    console.log(`Generated ${OUTPUT_FILE} (WARNING: core entries need manual restoration)`);
  }

  console.log(`  ${allEnvelopes.length} plugin envelope(s) from ${new Set(allEnvelopes.map(e => e.plugin)).size} plugin(s):`);
  for (const e of allEnvelopes) {
    console.log(`    ${e.type} -> ${e.component} (${e.plugin})`);
  }
}

main();
