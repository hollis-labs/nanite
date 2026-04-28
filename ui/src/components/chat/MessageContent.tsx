import { useMemo, useCallback } from 'react'
import ReactMarkdown from 'react-markdown'
import remarkBreaks from 'remark-breaks'
import remarkGfm from 'remark-gfm'
import { ArtifactChip } from './ArtifactChip'
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
      className="absolute top-2 right-2 p-1.5 rounded-md bg-surface hover:bg-surface-hover text-fg-secondary hover:text-fg transition-colors opacity-0 group-hover:opacity-100"
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
    <div className="group relative my-3 rounded-sm overflow-hidden border border-border">
      {lang && (
        <div className="flex items-center justify-between px-3 py-1.5 bg-bg-elevated border-b border-border">
          <span className="text-xs text-fg-muted">{lang}</span>
        </div>
      )}
      <pre className="bg-bg p-4 overflow-x-auto">
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
          className="px-1.5 py-0.5 rounded bg-surface text-fg text-sm font-mono"
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
      if (href?.startsWith('artifact:')) {
        const name = href.slice('artifact:'.length)
        return <ArtifactChip name={name} />
      }
      return (
        <a
          href={href}
          target="_blank"
          rel="noopener noreferrer"
          className="text-primary hover:text-primary-hover underline underline-offset-2"
          {...props}
        >
          {children}
        </a>
      )
    },
    table({ children, ...props }) {
      return (
        <div className="overflow-x-auto my-3">
          <table className="w-full text-sm border-collapse border border-border-subtle" {...props}>
            {children}
          </table>
        </div>
      )
    },
    thead({ children, ...props }) {
      return <thead className="bg-surface/50" {...props}>{children}</thead>
    },
    th({ children, ...props }) {
      return (
        <th className="px-3 py-2 text-left text-xs font-medium text-fg-secondary border border-border-subtle" {...props}>
          {children}
        </th>
      )
    },
    td({ children, ...props }) {
      return (
        <td className="px-3 py-2 text-sm text-fg-secondary border border-border-subtle" {...props}>
          {children}
        </td>
      )
    },
    tr({ children, ...props }) {
      return <tr className="even:bg-surface/30" {...props}>{children}</tr>
    },
    blockquote({ children, ...props }) {
      return (
        <blockquote
          className="border-l-2 border-primary/50 bg-bg-elevated/50 pl-4 py-2 my-3 text-fg-secondary italic"
          {...props}
        >
          {children}
        </blockquote>
      )
    },
    h1({ children, ...props }) {
      return <h1 className="text-xl font-bold text-fg mt-6 mb-3" {...props}>{children}</h1>
    },
    h2({ children, ...props }) {
      return <h2 className="text-lg font-semibold text-fg mt-5 mb-2" {...props}>{children}</h2>
    },
    h3({ children, ...props }) {
      return <h3 className="text-base font-semibold text-fg mt-4 mb-2" {...props}>{children}</h3>
    },
    h4({ children, ...props }) {
      return <h4 className="text-sm font-semibold text-fg mt-3 mb-1" {...props}>{children}</h4>
    },
    h5({ children, ...props }) {
      return <h5 className="text-sm font-medium text-fg-secondary mt-3 mb-1" {...props}>{children}</h5>
    },
    h6({ children, ...props }) {
      return <h6 className="text-xs font-medium text-fg-secondary mt-3 mb-1 uppercase tracking-wider" {...props}>{children}</h6>
    },
    ul({ children, ...props }) {
      return <ul className="list-disc list-outside pl-5 my-2 space-y-1 text-fg-secondary" {...props}>{children}</ul>
    },
    ol({ children, ...props }) {
      return <ol className="list-decimal list-outside pl-5 my-2 space-y-1 text-fg-secondary" {...props}>{children}</ol>
    },
    li({ children, ...props }) {
      return <li className="text-sm leading-relaxed [&>p]:inline" {...props}>{children}</li>
    },
    p({ children, ...props }) {
      return <p className="my-2 leading-relaxed" {...props}>{children}</p>
    },
    hr() {
      return <hr className="my-4 border-border-subtle" />
    },
    strong({ children, ...props }) {
      return <strong className="font-semibold text-fg" {...props}>{children}</strong>
    },
    em({ children, ...props }) {
      return <em className="italic text-fg-secondary" {...props}>{children}</em>
    },
  }), [])

  // Strip envelope blocks from content so they don't render as raw JSON.
  // Two passes:
  //   1. Remove complete (closed) fences — these are extracted as cards.
  //   2. Detect any remaining open fence (streaming, not yet closed) and strip
  //      from its start to end-of-string. Signal hasPendingEnvelope so a loading
  //      placeholder renders instead of the partial JSON.
  const { displayContent, hasPendingEnvelope } = useMemo(() => {
    let text = content
      .replace(/```(?:volon-envelope|nanite-envelope|fragments-envelope)\s*\n[\s\S]*?```/g, '')
      .replace(/<!--TICKET_DATA:[\s\S]*?:TICKET_DATA-->/g, '')
      .replace(/<!--ENVELOPE_DATA:[\s\S]*?:ENVELOPE_DATA-->/g, '')

    // After stripping closed fences, any remaining fence open-tag is incomplete.
    const openFence = /```(?:volon-envelope|nanite-envelope|fragments-envelope)/.exec(text)
    const hasPendingEnvelope = openFence !== null
    if (openFence) {
      text = text.slice(0, openFence.index)
    }

    return { displayContent: text.trim(), hasPendingEnvelope }
  }, [content])

  if (role === 'user') {
    return (
      <div className="text-sm text-fg leading-relaxed whitespace-pre-wrap">
        {displayContent}
      </div>
    )
  }

  return (
    <div className="text-sm text-fg leading-relaxed prose-dark">
      <ReactMarkdown remarkPlugins={[remarkGfm, remarkBreaks]} components={components}>
        {displayContent}
      </ReactMarkdown>
      {hasPendingEnvelope && (
        <div className="rounded-md border border-border-subtle bg-bg-elevated px-3 py-2.5 mt-2 flex items-center gap-2">
          <div className="w-1.5 h-1.5 rounded-full bg-primary/50 animate-pulse" />
          <span className="text-xs text-fg-muted">Preparing card…</span>
        </div>
      )}
    </div>
  )
}
