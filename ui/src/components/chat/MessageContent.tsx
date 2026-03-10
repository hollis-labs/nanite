import { useMemo, useCallback } from 'react'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import hljs from 'highlight.js/lib/core'
import javascript from 'highlight.js/lib/languages/javascript'
import typescript from 'highlight.js/lib/languages/typescript'
import python from 'highlight.js/lib/languages/python'
import go from 'highlight.js/lib/languages/go'
import bash from 'highlight.js/lib/languages/bash'
import json from 'highlight.js/lib/languages/json'
import yaml from 'highlight.js/lib/languages/yaml'
import sql from 'highlight.js/lib/languages/sql'
import css from 'highlight.js/lib/languages/css'
import xml from 'highlight.js/lib/languages/xml'
import markdown from 'highlight.js/lib/languages/markdown'
import { Copy, Check } from 'lucide-react'
import { useState } from 'react'
import type { Components } from 'react-markdown'

// Register languages
hljs.registerLanguage('javascript', javascript)
hljs.registerLanguage('js', javascript)
hljs.registerLanguage('typescript', typescript)
hljs.registerLanguage('ts', typescript)
hljs.registerLanguage('python', python)
hljs.registerLanguage('py', python)
hljs.registerLanguage('go', go)
hljs.registerLanguage('golang', go)
hljs.registerLanguage('bash', bash)
hljs.registerLanguage('sh', bash)
hljs.registerLanguage('shell', bash)
hljs.registerLanguage('json', json)
hljs.registerLanguage('yaml', yaml)
hljs.registerLanguage('yml', yaml)
hljs.registerLanguage('sql', sql)
hljs.registerLanguage('css', css)
hljs.registerLanguage('html', xml)
hljs.registerLanguage('xml', xml)
hljs.registerLanguage('markdown', markdown)
hljs.registerLanguage('md', markdown)

function CopyButton({ text }: { text: string }) {
  const [copied, setCopied] = useState(false)

  const handleCopy = useCallback(() => {
    void navigator.clipboard.writeText(text)
    setCopied(true)
    setTimeout(() => setCopied(false), 2000)
  }, [text])

  return (
    <button
      onClick={handleCopy}
      className="absolute top-2 right-2 p-1.5 rounded-md bg-zinc-800 hover:bg-zinc-700 text-zinc-400 hover:text-zinc-200 transition-colors opacity-0 group-hover:opacity-100"
      aria-label="Copy code"
    >
      {copied ? <Check className="w-3.5 h-3.5" /> : <Copy className="w-3.5 h-3.5" />}
    </button>
  )
}

function CodeBlock({ className, children }: { className?: string; children: React.ReactNode }) {
  const text = String(children).replace(/\n$/, '')
  const langMatch = className?.match(/language-(\w+)/)
  const lang = langMatch?.[1]

  let highlighted: string
  try {
    if (lang && hljs.getLanguage(lang)) {
      highlighted = hljs.highlight(text, { language: lang }).value
    } else {
      highlighted = hljs.highlightAuto(text).value
    }
  } catch {
    highlighted = text
  }

  return (
    <div className="group relative my-3 rounded-lg overflow-hidden border border-zinc-800">
      {lang && (
        <div className="flex items-center justify-between px-3 py-1.5 bg-zinc-900 border-b border-zinc-800">
          <span className="text-xs text-zinc-500">{lang}</span>
        </div>
      )}
      <pre className="bg-zinc-950 p-4 overflow-x-auto">
        <code
          className="text-sm leading-relaxed"
          dangerouslySetInnerHTML={{ __html: highlighted }}
        />
      </pre>
      <CopyButton text={text} />
    </div>
  )
}

export function MessageContent({ content, role }: { content: string; role: 'user' | 'assistant' | 'system' | 'tool' }) {
  const components: Components = useMemo(() => ({
    code({ className, children, ...props }) {
      // Check if this is a code block (inside a pre) vs inline code
      const isBlock = className?.startsWith('language-')
      if (isBlock) {
        return <CodeBlock className={className}>{children}</CodeBlock>
      }
      return (
        <code
          className="px-1.5 py-0.5 rounded bg-zinc-800 text-zinc-200 text-sm font-mono"
          {...props}
        >
          {children}
        </code>
      )
    },
    pre({ children }) {
      // The code block component handles its own pre wrapper
      return <>{children}</>
    },
    a({ href, children, ...props }) {
      return (
        <a
          href={href}
          target="_blank"
          rel="noopener noreferrer"
          className="text-indigo-400 hover:text-indigo-300 underline underline-offset-2"
          {...props}
        >
          {children}
        </a>
      )
    },
    table({ children, ...props }) {
      return (
        <div className="overflow-x-auto my-3">
          <table className="w-full text-sm border-collapse border border-zinc-700" {...props}>
            {children}
          </table>
        </div>
      )
    },
    thead({ children, ...props }) {
      return <thead className="bg-zinc-800/50" {...props}>{children}</thead>
    },
    th({ children, ...props }) {
      return (
        <th className="px-3 py-2 text-left text-xs font-medium text-zinc-300 border border-zinc-700" {...props}>
          {children}
        </th>
      )
    },
    td({ children, ...props }) {
      return (
        <td className="px-3 py-2 text-sm text-zinc-300 border border-zinc-700" {...props}>
          {children}
        </td>
      )
    },
    tr({ children, ...props }) {
      return <tr className="even:bg-zinc-800/30" {...props}>{children}</tr>
    },
    blockquote({ children, ...props }) {
      return (
        <blockquote
          className="border-l-2 border-indigo-500/50 bg-zinc-900/50 pl-4 py-2 my-3 text-zinc-300 italic"
          {...props}
        >
          {children}
        </blockquote>
      )
    },
    h1({ children, ...props }) {
      return <h1 className="text-xl font-bold text-zinc-100 mt-6 mb-3" {...props}>{children}</h1>
    },
    h2({ children, ...props }) {
      return <h2 className="text-lg font-semibold text-zinc-100 mt-5 mb-2" {...props}>{children}</h2>
    },
    h3({ children, ...props }) {
      return <h3 className="text-base font-semibold text-zinc-200 mt-4 mb-2" {...props}>{children}</h3>
    },
    h4({ children, ...props }) {
      return <h4 className="text-sm font-semibold text-zinc-200 mt-3 mb-1" {...props}>{children}</h4>
    },
    h5({ children, ...props }) {
      return <h5 className="text-sm font-medium text-zinc-300 mt-3 mb-1" {...props}>{children}</h5>
    },
    h6({ children, ...props }) {
      return <h6 className="text-xs font-medium text-zinc-400 mt-3 mb-1 uppercase tracking-wider" {...props}>{children}</h6>
    },
    ul({ children, ...props }) {
      return <ul className="list-disc list-inside my-2 space-y-1 text-zinc-300" {...props}>{children}</ul>
    },
    ol({ children, ...props }) {
      return <ol className="list-decimal list-inside my-2 space-y-1 text-zinc-300" {...props}>{children}</ol>
    },
    li({ children, ...props }) {
      return <li className="text-sm leading-relaxed" {...props}>{children}</li>
    },
    p({ children, ...props }) {
      return <p className="my-2 leading-relaxed" {...props}>{children}</p>
    },
    hr() {
      return <hr className="my-4 border-zinc-700" />
    },
    strong({ children, ...props }) {
      return <strong className="font-semibold text-zinc-100" {...props}>{children}</strong>
    },
    em({ children, ...props }) {
      return <em className="italic text-zinc-300" {...props}>{children}</em>
    },
  }), [])

  // Strip envelope blocks from content so they don't render as raw JSON
  // (during streaming, envelopes haven't been extracted yet).
  const displayContent = useMemo(() =>
    content.replace(/```(?:volon-envelope|mentat-envelope)\s*\n[\s\S]*?```/g, '').trim(),
    [content]
  )

  if (role === 'user') {
    return (
      <div className="text-sm text-zinc-200 leading-relaxed whitespace-pre-wrap">
        {content}
      </div>
    )
  }

  return (
    <div className="text-sm text-zinc-200 leading-relaxed prose-dark">
      <ReactMarkdown remarkPlugins={[remarkGfm]} components={components}>
        {displayContent}
      </ReactMarkdown>
    </div>
  )
}
