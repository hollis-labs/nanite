#!/usr/bin/env node
/**
 * Envelope Type Generator
 *
 * Reads JSON Schema files from a manifest schemas/ directory and generates
 * TypeScript interfaces + a type-safe registry mapping.
 *
 * Source of truth (post-migration to go-envelopes v0.1.0): the lib's
 * manifest at ../go-envelopes/manifest/schemas/. The Makefile passes
 * --manifest-dir to point at the lib; tests / ad-hoc invocations may
 * pass any directory of *.schema.json files.
 *
 * Output: ui/src/generated/envelope-types.generated.ts
 *
 * Usage:
 *   node scripts/generate-envelope-types.mjs --manifest-dir <path>
 *   node scripts/generate-envelope-types.mjs --manifest-dir <path> --check  (staleness check, exits 1 if stale)
 *
 * Default --manifest-dir is ../go-envelopes/manifest/schemas/ relative
 * to the nanite repo root, matching the local-replace setup. Override
 * for tests or alternate vendoring.
 */
import { readFileSync, readdirSync, writeFileSync, existsSync, statSync } from 'node:fs';
import { join, resolve, dirname, isAbsolute } from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = dirname(fileURLToPath(import.meta.url));
const ROOT = resolve(__dirname, '..');
const OUTPUT_FILE = join(ROOT, 'ui', 'src', 'generated', 'envelope-types.generated.ts');

const CHECK_MODE = process.argv.includes('--check');

function parseManifestDir() {
  const flagIdx = process.argv.indexOf('--manifest-dir');
  if (flagIdx === -1) {
    return resolve(ROOT, '..', 'go-envelopes', 'manifest', 'schemas');
  }
  const value = process.argv[flagIdx + 1];
  if (!value || value.startsWith('--')) {
    console.error('--manifest-dir requires a path argument');
    process.exit(1);
  }
  return isAbsolute(value) ? value : resolve(ROOT, value);
}

const SCHEMA_DIR = parseManifestDir();
if (!existsSync(SCHEMA_DIR) || !statSync(SCHEMA_DIR).isDirectory()) {
  console.error('Manifest schemas directory not found:', SCHEMA_DIR);
  console.error('Pass --manifest-dir <path> pointing at a directory of *.schema.json files.');
  process.exit(1);
}

// --- Schema loading ---

function loadSchemas() {
  const files = readdirSync(SCHEMA_DIR).filter(f => f.endsWith('.schema.json')).sort();
  if (files.length === 0) {
    console.error('No schema files found in', SCHEMA_DIR);
    process.exit(1);
  }
  return files.map(file => {
    const raw = readFileSync(join(SCHEMA_DIR, file), 'utf-8');
    const schema = JSON.parse(raw);
    const typeName = file.replace('.schema.json', '');
    return { typeName, schema, file };
  });
}

// --- Type name generation ---

/** Convert "info-card" to "InfoCardData" */
function toInterfaceName(typeName) {
  return typeName
    .split('-')
    .map(part => part.charAt(0).toUpperCase() + part.slice(1))
    .join('') + 'Data';
}

// --- Schema-to-TypeScript conversion ---

function jsonTypeToTS(prop, required = false) {
  if (!prop) return 'unknown';

  // oneOf with type alternatives
  if (prop.oneOf) {
    return prop.oneOf.map(alt => jsonTypeToTS(alt)).join(' | ');
  }

  // $ref (local only)
  if (prop.$ref) {
    const refName = prop.$ref.split('/').pop();
    // We'll generate these as nested interfaces
    return refName;
  }

  if (prop.enum) {
    return prop.enum.map(v => JSON.stringify(v)).join(' | ');
  }

  switch (prop.type) {
    case 'string':
      return 'string';
    case 'number':
    case 'integer':
      return 'number';
    case 'boolean':
      return 'boolean';
    case 'null':
      return 'null';
    case 'array':
      if (prop.items) {
        const itemType = jsonTypeToTS(prop.items);
        return `${itemType}[]`;
      }
      return 'unknown[]';
    case 'object':
      if (prop.properties) {
        return generateInlineObject(prop);
      }
      if (prop.additionalProperties === true) {
        return 'Record<string, unknown>';
      }
      if (typeof prop.additionalProperties === 'object') {
        const valType = jsonTypeToTS(prop.additionalProperties);
        return `Record<string, ${valType}>`;
      }
      return 'Record<string, unknown>';
    default:
      return 'unknown';
  }
}

function generateInlineObject(schema) {
  const requiredSet = new Set(schema.required || []);
  const lines = [];
  for (const [key, prop] of Object.entries(schema.properties || {})) {
    const optional = requiredSet.has(key) ? '' : '?';
    const tsType = jsonTypeToTS(prop);
    lines.push(`${key}${optional}: ${tsType}`);
  }
  return `{ ${lines.join('; ')} }`;
}

function generateInterface(name, schema, indent = '') {
  const lines = [];
  const requiredSet = new Set(schema.required || []);

  lines.push(`${indent}export interface ${name} {`);

  for (const [key, prop] of Object.entries(schema.properties || {})) {
    const optional = requiredSet.has(key) ? '' : '?';
    const tsType = jsonTypeToTS(prop);
    if (prop.description) {
      lines.push(`${indent}  /** ${prop.description} */`);
    }
    lines.push(`${indent}  ${key}${optional}: ${tsType};`);
  }

  lines.push(`${indent}}`);
  return lines.join('\n');
}

// --- Code generation ---

function generate(schemas) {
  const lines = [];

  lines.push('// AUTO-GENERATED FILE — DO NOT EDIT MANUALLY');
  lines.push('// Generated by scripts/generate-envelope-types.mjs from the go-envelopes manifest schemas/.');
  lines.push('// To regenerate: make generate-envelopes (or pass --manifest-dir <path> to the script directly).');
  lines.push('');

  // Generate $defs first (shared types)
  const defsEmitted = new Set();
  for (const { schema, typeName } of schemas) {
    if (schema.$defs) {
      for (const [defName, defSchema] of Object.entries(schema.$defs)) {
        if (defsEmitted.has(defName)) continue;
        defsEmitted.add(defName);
        lines.push(`/** Shared type used by ${typeName} */`);
        lines.push(generateInterface(defName, defSchema));
        lines.push('');
      }
    }
  }

  // Generate data interfaces
  for (const { typeName, schema } of schemas) {
    const interfaceName = toInterfaceName(typeName);
    lines.push(`/** Envelope data for "${typeName}" — ${schema.description || schema.title} */`);

    // Resolve $ref in properties to the def name
    const resolvedSchema = { ...schema };
    if (resolvedSchema.properties) {
      const resolvedProps = { ...resolvedSchema.properties };
      for (const [key, prop] of Object.entries(resolvedProps)) {
        if (prop.items && prop.items.$ref) {
          const refName = prop.items.$ref.split('/').pop();
          resolvedProps[key] = { ...prop, items: { type: 'object', _refName: refName } };
        }
      }
      resolvedSchema.properties = resolvedProps;
    }

    lines.push(generateInterface(interfaceName, schema));
    lines.push('');
  }

  // Generate union type of all envelope type strings
  lines.push('/** Union of all registered envelope type strings. */');
  lines.push('export type EnvelopeType =');
  for (let i = 0; i < schemas.length; i++) {
    const sep = i < schemas.length - 1 ? ' |' : ';';
    lines.push(`  | "${schemas[i].typeName}"${i === schemas.length - 1 ? ';' : ''}`);
  }
  lines.push('');

  // Generate type map
  lines.push('/** Maps each envelope type string to its data interface. */');
  lines.push('export interface EnvelopeDataMap {');
  for (const { typeName } of schemas) {
    const interfaceName = toInterfaceName(typeName);
    lines.push(`  "${typeName}": ${interfaceName};`);
  }
  lines.push('}');
  lines.push('');

  // Export the list of all types for runtime use
  lines.push('/** All registered envelope type strings. */');
  lines.push('export const ENVELOPE_TYPES: EnvelopeType[] = [');
  for (const { typeName } of schemas) {
    lines.push(`  "${typeName}",`);
  }
  lines.push('];');
  lines.push('');

  return lines.join('\n');
}

// --- Main ---

const schemas = loadSchemas();
const output = generate(schemas);

if (CHECK_MODE) {
  if (!existsSync(OUTPUT_FILE)) {
    console.error('Generated file does not exist:', OUTPUT_FILE);
    console.error('Run: node scripts/generate-envelope-types.mjs');
    process.exit(1);
  }
  const existing = readFileSync(OUTPUT_FILE, 'utf-8');
  if (existing !== output) {
    console.error('Generated envelope types are stale.');
    console.error('Run: node scripts/generate-envelope-types.mjs');
    process.exit(1);
  }
  console.log('Envelope types are up to date.');
  process.exit(0);
}

writeFileSync(OUTPUT_FILE, output, 'utf-8');
console.log(`Generated ${schemas.length} envelope type definitions to ${OUTPUT_FILE}`);
