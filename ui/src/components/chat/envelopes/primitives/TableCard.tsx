import { useState } from 'react'
import { ArrowUp, ArrowDown, Table as TableIcon } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { ResponseStatus } from '@/lib/envelope-response'
import type { ResponseV1 } from '@/lib/envelope-response'
import { Envelope, EnvelopeHeader } from './Envelope'
import type { EnvelopeResponder } from '../EnvelopeRenderer'

/**
 * A row/column action, schema-validated against table-card.schema.json's
 * $defs/action (Phase 6 — interactive-table row-actions primitive; see
 * docs/engineering/architecture/08-cards.md). A narrow, deliberately-not-
 * copied-wholesale subset of Adaptive Cards' `Action.Submit` model: an id
 * (echoed back to the backend, never shown), a label, optional visual
 * emphasis, and an optional client-side confirmation gate.
 */
interface TableAction {
  id: string
  label: string
  style?: 'default' | 'primary' | 'destructive'
  confirm?: boolean
  confirm_message?: string
}

interface TableColumn {
  key: string
  label: string
  sortable?: boolean
  /** Column-scoped actions, rendered inline within this column's cells. */
  actions?: TableAction[]
}

type TableRow = Record<string, string | number | boolean | null>

interface TableCardData {
  title?: string
  columns: TableColumn[]
  rows: TableRow[]
  caption?: string
  /** Row-scoped actions, rendered as a button group on every row. */
  actions?: TableAction[]
  /**
   * Set by EnvelopeRenderer's default `{ data }` prop-shape branch when this
   * envelope was already responded to (merged in from the wrap-level
   * `prior_response` field on reload — CW-20260517-0006's mechanism, table-
   * card has no `props: "envelope"` discriminator so it never sees the full
   * envelope, only this merged-in field).
   */
  prior_response?: ResponseV1
}

interface TableCardProps {
  data: TableCardData
  /** Present only when the envelope has an id — see EnvelopeRenderer.tsx. */
  onRespond?: EnvelopeResponder
}

const ACTION_VARIANT: Record<NonNullable<TableAction['style']>, 'outline' | 'default' | 'destructive'> = {
  default: 'outline',
  primary: 'default',
  destructive: 'destructive',
}

export function TableCard({ data, onRespond }: TableCardProps) {
  const [sortKey, setSortKey] = useState<string | null>(null)
  const [sortAsc, setSortAsc] = useState(true)
  const [pendingKey, setPendingKey] = useState<string | null>(null)
  const [actionError, setActionError] = useState<string | null>(null)

  const handleSort = (key: string) => {
    if (sortKey === key) {
      setSortAsc((prev) => !prev)
    } else {
      setSortKey(key)
      setSortAsc(true)
    }
  }

  // `columns`/`rows` are required by the type, but a partially-loaded
  // envelope can arrive without them — normalize so the card degrades
  // gracefully instead of crashing the renderer.
  const columns = data.columns ?? []
  const rows = data.rows ?? []

  // A previously-recorded response means this envelope's respond endpoint
  // is already claimed (a second POST would 409) — degrade to read-only
  // instead of offering dead buttons.
  const alreadyResponded = data.prior_response != null
  // No onRespond means this envelope has no id (the passive card_show
  // path table-card also supports) — there is nowhere to POST a response,
  // so action affordances are not rendered at all rather than shown as
  // permanently-dead buttons. Once responded, buttons stay VISIBLE but
  // disabled so the user can see what was offered.
  const canRespond = Boolean(onRespond)
  const interactive = canRespond && !alreadyResponded

  // Actions are only worth rendering at all when there is somewhere to
  // POST the response — a passive (no-id) table with an `actions` field
  // renders as a plain read-only table, same as one with no actions.
  const rowActions = canRespond ? (data.actions ?? []) : []
  const hasColumnActions = canRespond && columns.some((col) => (col.actions?.length ?? 0) > 0)

  // Sort while preserving each row's ORIGINAL index — action responses
  // reference row_index against the server-persisted (unsorted) row order,
  // so the visual sort must not renumber rows.
  const indexedRows = rows.map((row, originalIndex) => ({ row, originalIndex }))
  const sortedRows = [...indexedRows]
  if (sortKey) {
    sortedRows.sort((a, b) => {
      const av = a.row[sortKey]
      const bv = b.row[sortKey]
      if (av == null && bv == null) return 0
      if (av == null) return 1
      if (bv == null) return -1
      const sa = String(av)
      const sb = String(bv)
      if (sa < sb) return sortAsc ? -1 : 1
      if (sa > sb) return sortAsc ? 1 : -1
      return 0
    })
  }

  const runAction = (action: TableAction, rowIndex: number, row: TableRow, columnKey?: string) => {
    if (!onRespond) return
    if (action.confirm) {
      const message = action.confirm_message || `Run "${action.label}"?`
      if (!window.confirm(message)) return
    }
    const key = `${rowIndex}:${columnKey ?? ''}:${action.id}`
    setPendingKey(key)
    setActionError(null)
    void onRespond({
      status: ResponseStatus.Submitted,
      data: {
        action_id: action.id,
        row_index: rowIndex,
        row,
        ...(columnKey ? { column_key: columnKey } : {}),
      },
    })
      .catch((err) => {
        setActionError(err instanceof Error ? err.message : 'Failed to submit action')
      })
      .finally(() => {
        setPendingKey((current) => (current === key ? null : current))
      })
  }

  const renderActionButtons = (actions: TableAction[], rowIndex: number, row: TableRow, columnKey?: string) => (
    <span className="inline-flex flex-wrap items-center gap-1.5">
      {actions.map((action) => {
        const key = `${rowIndex}:${columnKey ?? ''}:${action.id}`
        return (
          <Button
            key={action.id}
            type="button"
            size="sm"
            variant={ACTION_VARIANT[action.style ?? 'default']}
            disabled={!interactive || pendingKey === key}
            onClick={() => runAction(action, rowIndex, row, columnKey)}
          >
            {action.label}
          </Button>
        )
      })}
    </span>
  )

  return (
    <Envelope>
      <EnvelopeHeader
        icon={TableIcon}
        label={data.title || 'Table'}
        meta={`${rows.length} row${rows.length === 1 ? '' : 's'}`}
      />
      <div className="overflow-x-auto">
        <table className="w-full text-[13px]">
          <thead>
            <tr className="border-b border-border-subtle">
              {columns.map((col) => (
                <th
                  key={col.key}
                  className="px-4 py-2 text-left font-mono text-[10px] font-semibold uppercase tracking-wide text-fg-muted"
                  aria-sort={
                    col.sortable
                      ? sortKey === col.key
                        ? (sortAsc ? 'ascending' : 'descending')
                        : 'none'
                      : undefined
                  }
                >
                  {col.sortable ? (
                    <button
                      type="button"
                      className="inline-flex w-full select-none items-center gap-1 text-left hover:text-fg-secondary"
                      onClick={() => handleSort(col.key)}
                    >
                      <span>{col.label}</span>
                      {sortKey === col.key && (
                        sortAsc
                          ? <ArrowUp className="h-3 w-3" />
                          : <ArrowDown className="h-3 w-3" />
                      )}
                    </button>
                  ) : (
                    <span>{col.label}</span>
                  )}
                </th>
              ))}
              {rowActions.length > 0 && (
                <th className="px-4 py-2 text-left font-mono text-[10px] font-semibold uppercase tracking-wide text-fg-muted">
                  Actions
                </th>
              )}
            </tr>
          </thead>
          <tbody>
            {sortedRows.map(({ row, originalIndex }) => (
              <tr
                key={`row-${originalIndex}`}
                className="border-b border-border-subtle last:border-0"
              >
                {columns.map((col) => (
                  <td key={col.key} className="px-4 py-2 text-fg-secondary">
                    <span className="inline-flex flex-wrap items-center gap-2">
                      <span>{String(row[col.key] ?? '')}</span>
                      {canRespond && col.actions?.length
                        ? renderActionButtons(col.actions, originalIndex, row, col.key)
                        : null}
                    </span>
                  </td>
                ))}
                {rowActions.length > 0 && (
                  <td className="px-4 py-2">
                    {renderActionButtons(rowActions, originalIndex, row)}
                  </td>
                )}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {actionError && (
        <div
          role="alert"
          className="mx-4 mb-3 rounded-md border border-danger/30 bg-danger/5 px-3 py-2 text-[12px] text-danger"
        >
          {actionError}
        </div>
      )}
      {alreadyResponded && (rowActions.length > 0 || hasColumnActions) && (
        <div className="px-4 pb-3 text-[11px] text-fg-muted">
          An action was already recorded for this table.
        </div>
      )}
      {data.caption && (
        <div className="border-t border-border-subtle px-4 py-2">
          <p className="text-[11px] text-fg-muted">{data.caption}</p>
        </div>
      )}
    </Envelope>
  )
}
