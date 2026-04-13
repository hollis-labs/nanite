import { defineConfig, type Plugin } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import path from 'path'
import { readFileSync } from 'fs'
import { createRequire } from 'module'

const HOST_ENTRIES = {
  react: 'src/_host/react.ts',
  'react-dom': 'src/_host/react-dom.ts',
  'react-dom/client': 'src/_host/react-dom-client.ts',
  'react/jsx-runtime': 'src/_host/react-jsx-runtime.ts',
} as const

// Entry-point name → bare specifier reverse lookup.
const SPECIFIER_BY_ENTRY: Record<string, string> = Object.fromEntries(
  Object.entries(HOST_ENTRIES).map(([spec, file]) => {
    const base = path.basename(file, path.extname(file)) // e.g. "react-dom-client"
    return [base, spec]
  }),
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

  return {
    name: 'nanite-host-importmap',
    enforce: 'post',
    apply: 'build',

    buildStart: {
      order: 'pre',
      handler() {
        // Emit es-module-shims as a static asset with a fixed name so the
        // built site is self-contained (no CDN dependency) and so
        // transformIndexHtml can reference it by path.
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

        const tags = [
          `<script async src="/${shimsFileName}"></script>`,
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
    port: 5176,
    proxy: {
      '/api': {
        target: 'http://localhost:8090',
        changeOrigin: true,
      },
    },
  },
  build: {
    outDir: 'dist',
    rollupOptions: {
      input: {
        main: path.resolve(__dirname, 'index.html'),
        'react': path.resolve(__dirname, 'src/_host/react.ts'),
        'react-dom': path.resolve(__dirname, 'src/_host/react-dom.ts'),
        'react-dom-client': path.resolve(__dirname, 'src/_host/react-dom-client.ts'),
        'react-jsx-runtime': path.resolve(__dirname, 'src/_host/react-jsx-runtime.ts'),
      },
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
