#!/usr/bin/env node
/**
 * Generate Nanite's host-neutral envelope data types from the exact released
 * go-envelopes version selected by this checkout's go.mod.
 */
import { existsSync, readFileSync, writeFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

import { exportTypeScript } from './lib/envelope-catalog.mjs';

const ROOT = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const OUTPUT_FILE = join(ROOT, 'ui', 'src', 'generated', 'envelope-types.generated.ts');
const CHECK_MODE = process.argv.includes('--check');

let output;
try {
  output = exportTypeScript(ROOT);
} catch (error) {
  console.error(`Envelope type generation failed: ${error.message}`);
  process.exit(1);
}

if (CHECK_MODE) {
  if (!existsSync(OUTPUT_FILE) || readFileSync(OUTPUT_FILE, 'utf8') !== output) {
    console.error('Generated envelope types are stale.');
    console.error('Run: npm run generate:envelopes');
    process.exit(1);
  }
  console.log('Envelope types are up to date.');
  process.exit(0);
}

writeFileSync(OUTPUT_FILE, output, 'utf8');
console.log(`Generated envelope types to ${OUTPUT_FILE}`);
