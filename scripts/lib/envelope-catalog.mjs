import { spawnSync } from 'node:child_process';

const MODULE = 'github.com/hollis-labs/go-envelopes';
const EXPORT_COMMAND = `${MODULE}/cmd/envelopes-export`;

function runExporter(root, format) {
  const result = spawnSync(
    'go',
    ['run', EXPORT_COMMAND, '-format', format, '-output', '-'],
    {
      cwd: root,
      encoding: 'utf8',
      maxBuffer: 32 * 1024 * 1024,
    },
  );

  if (result.error) {
    throw new Error(`failed to start go-envelopes exporter: ${result.error.message}`);
  }
  if (result.status !== 0) {
    const detail = result.stderr.trim() || `exit status ${result.status}`;
    throw new Error(`go-envelopes exporter failed: ${detail}`);
  }
  if (!result.stdout) {
    throw new Error('go-envelopes exporter returned empty output');
  }
  return result.stdout;
}

function validateReleasedSource(source) {
  if (!source || source.module !== MODULE) {
    throw new Error(`unexpected envelope catalog source module: ${source?.module ?? '<missing>'}`);
  }
  if (!/^v\d+\.\d+\.\d+(?:[-+].*)?$/.test(source.moduleVersion ?? '')) {
    throw new Error(
      `envelope catalog must come from a released module version; got ${source.moduleVersion ?? '<missing>'}`,
    );
  }
  if (!source.manifestDigest?.startsWith('sha256:')) {
    throw new Error('envelope catalog is missing its manifest digest');
  }
}

export function exportCatalog(root) {
  const raw = runExporter(root, 'catalog');
  let catalog;
  try {
    catalog = JSON.parse(raw);
  } catch (error) {
    throw new Error(`go-envelopes exporter returned invalid catalog JSON: ${error.message}`);
  }
  if (catalog.formatVersion !== 1) {
    throw new Error(`unsupported envelope catalog format ${catalog.formatVersion ?? '<missing>'}`);
  }
  validateReleasedSource(catalog.source);
  if (!Array.isArray(catalog.types) || catalog.types.length === 0) {
    throw new Error('envelope catalog contains no registered types');
  }
  return catalog;
}

export function exportTypeScript(root) {
  const output = runExporter(root, 'typescript');
  const identity = output.match(
    new RegExp(`^// Source: ${MODULE.replaceAll('/', '\\/')}@([^;]+);.*manifest (sha256:[0-9a-f]+)\\.$`, 'm'),
  );
  if (!identity) {
    throw new Error('generated TypeScript is missing go-envelopes source identity');
  }
  validateReleasedSource({
    module: MODULE,
    moduleVersion: identity[1],
    manifestDigest: identity[2],
  });
  return output;
}
