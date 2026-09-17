import { defineConfig, type Plugin } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import path from 'path'
import { readFileSync } from 'fs'
import { createRequire } from 'module'

// J.5 OQ9: shared shadcn/Radix primitives. Plugins resolve
// `@nanite/ui/<primitive>` to the host's compiled component via the
// importmap — no per-plugin bundling of shadcn internals, no duplicate
// Radix roots, and a single theme-aware codepath.
const SHADCN_PRIMITIVES = [
  'button',
  'card',
  'dialog',
  'dropdown-menu',
  'popover',
  'select',
  'tooltip',
  'input',
  'textarea',
  'scroll-area',
  'separator',
] as const

const SHADCN_ENTRIES: Record<string, string> = Object.fromEntries(
  SHADCN_PRIMITIVES.map((p) => [`@nanite/ui/${p}`, `src/_host/shadcn/${p}.ts`]),
)

const HOST_ENTRIES = {
  react: 'src/_host/react.ts',
  'react-dom': 'src/_host/react-dom.ts',
  'react-dom/client': 'src/_host/react-dom-client.ts',
  'react/jsx-runtime': 'src/_host/react-jsx-runtime.ts',
  ...SHADCN_ENTRIES,
} as const

// Entry-point name → bare specifier reverse lookup. The rollup build emits
// an entry per file; its `chunk.name` is the input key we register below.
// Shadcn entries land under the rollup input name `shadcn-<primitive>` to
// keep the host chunk filenames flat (assets/_host/shadcn-button-<hash>.js).
function rollupInputName(file: string): string {
  // src/_host/shadcn/button.ts → shadcn-button
  // src/_host/react.ts         → react
  const base = path.basename(file, path.extname(file))
  if (file.includes('_host/shadcn/')) return `shadcn-${base}`
  return base
}

const SPECIFIER_BY_ENTRY: Record<string, string> = Object.fromEntries(
  Object.entries(HOST_ENTRIES).map(([spec, file]) => [rollupInputName(file), spec]),
)

/**
 * Emits the es-module-shims loader into the build output and injects an
 * importmap into index.html that maps bare React specifiers to the host's
 * `_host/*` entry chunks. Plugins loaded at runtime via `import(bundleUrl)`
 * then resolve `import React from 'react'` to the host's shared React.
 */
function hostImportmapPlugin(): Plugin {
  const require = createRequire(import.meta.url)
  const shimsFileName = 'assets/es-module-shims.js'
  let isBuild = false

  return {
    name: 'nanite-host-importmap',
    enforce: 'post',

    configResolved(config) {
      isBuild = config.command === 'build'
    },

    buildStart: {
      order: 'pre',
      handler() {
        // Only emit the shim as an asset during production builds. In dev,
        // native importmaps are injected via transformIndexHtml and Vite's
        // dev server serves _host/*.ts directly — no shim needed.
        if (!isBuild) return
        const shimsSource = readFileSync(require.resolve('es-module-shims'), 'utf-8')
        // biome-ignore lint/suspicious/noExplicitAny: Vite 7's plugin `this` type omits emitFile; Rollup's PluginContext provides it at runtime.
        ;(this as any).emitFile({
          type: 'asset',
          fileName: shimsFileName,
          source: shimsSource,
        })
      },
    },

    transformIndexHtml: {
      order: 'post',
      handler(html, ctx) {
        // Dev mode: Vite's middleware serves /src/_host/*.ts through its
        // transform pipeline. We still inject an importmap so dynamically
        // imported plugin bundles can resolve bare specifiers in dev.
        if (!ctx.bundle) {
          const devImports = Object.fromEntries(
            Object.entries(HOST_ENTRIES).map(([spec, file]) => [spec, `/${file}`]),
          )
          const devTag = `<script type="importmap">${JSON.stringify({ imports: devImports })}</script>`
          return html.replace('</head>', `    ${devTag}\n  </head>`)
        }

        // Look up the emitted entry chunk for each host specifier.
        const imports: Record<string, string> = {}
        for (const [fileName, chunk] of Object.entries(ctx.bundle)) {
          if (chunk.type !== 'chunk' || !chunk.isEntry) continue
          const spec = SPECIFIER_BY_ENTRY[chunk.name]
          if (spec) imports[spec] = `/${fileName}`
        }

        // `src` without `async` so the shim runs before any module script
        // that depends on importmap resolution in browsers that need it.
        const tags = [
          `<script src="/${shimsFileName}"></script>`,
          `<script type="importmap">${JSON.stringify({ imports })}</script>`,
        ].join('\n    ')

        // Insert before the first Vite-injected module script so the
        // importmap is active when the main bundle loads.
        const mainScriptMatch = html.match(/<script[^>]*type="module"[^>]*>/)
        if (mainScriptMatch && mainScriptMatch.index !== undefined) {
          return html.slice(0, mainScriptMatch.index)
            + tags
            + '\n    '
            + html.slice(mainScriptMatch.index)
        }
        return html.replace('</head>', `    ${tags}\n  </head>`)
      },
    },
  }
}

export default defineConfig({
  plugins: [react(), tailwindcss(), hostImportmapPlugin()],
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src'),
    },
  },
  server: {
    port: Number(process.env.NANITE_UI_PORT) || 5176,
    proxy: {
      '/api': {
        // Points at `nanite serve`. Overridable because 8090 is a popular
        // port — a container from an unrelated project holding it should not
        // mean editing a tracked file to run the dev server.
        target: `http://localhost:${process.env.NANITE_API_PORT || 8090}`,
        changeOrigin: true,
      },
    },
  },
  build: {
    outDir: 'dist',
    rollupOptions: {
      input: {
        main: path.resolve(__dirname, 'index.html'),
        // Derive host-entry inputs from HOST_ENTRIES so the shadcn primitives
        // and React re-exports stay in one list.
        ...Object.fromEntries(
          Object.values(HOST_ENTRIES).map((file) => [
            rollupInputName(file),
            path.resolve(__dirname, file),
          ]),
        ),
      },
      // Host-side consumers don't reference every React export we want
      // plugins to see, so Rollup's default `exports-only` signature
      // preservation tree-shakes them out of the emitted chunks. Plugin
      // bundles resolve bare `react*` specifiers through the importmap at
      // runtime, not statically — `strict` keeps the full export surface
      // intact on the entry chunks so runtime imports find every name
      // (BLG-20260414-011).
      preserveEntrySignatures: 'strict',
      output: {
        entryFileNames: (chunk) => {
          if (SPECIFIER_BY_ENTRY[chunk.name]) {
            return 'assets/_host/[name]-[hash].js'
          }
          return 'assets/[name]-[hash].js'
        },
      },
    },
  },
})
