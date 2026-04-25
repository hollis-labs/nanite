import { useState } from 'react'
import { ArrowUp, ArrowDown, Table as TableIcon } from 'lucide-react'
import { Envelope, EnvelopeHeader } from './Envelope'

interface TableColumn {
  key: string
  label: string
  sortable?: boolean
}

interface TableCardData {
  title?: string
  columns: TableColumn[]
  rows: Array<Record<string, string | number | boolean | null>>
  caption?: string
}

interface TableCardProps {
  data: TableCardData
}

export function TableCard({ data }: TableCardProps) {
  const [sortKey, setSortKey] = useState<string | null>(null)
  const [sortAsc, setSortAsc] = useState(true)

  const handleSort = (key: string) => {
    if (sortKey === key) {
      setSortAsc((prev) => !prev)
    } else {
      setSortKey(key)
      setSortAsc(true)
    }
  }

  const sortedRows = [...data.rows]
  if (sortKey) {
    sortedRows.sort((a, b) => {
      const av = a[sortKey]
      const bv = b[sortKey]
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

  return (
    <Envelope>
      <EnvelopeHeader
        icon={TableIcon}
        label={data.title || 'Table'}
        meta={`${data.rows.length} row${data.rows.length === 1 ? '' : 's'}`}
      />
      <div className="overflow-x-auto">
        <table className="w-full text-[13px]">
          <thead>
            <tr className="border-b border-border-subtle">
              {data.columns.map((col) => (
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
            </tr>
          </thead>
          <tbody>
            {sortedRows.map((row, ri) => (
              <tr
                key={`row-${ri}`}
                className="border-b border-border-subtle last:border-0"
              >
                {data.columns.map((col) => (
                  <td key={col.key} className="px-4 py-2 text-fg-secondary">
                    {String(row[col.key] ?? '')}
                  </td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {data.caption && (
        <div className="border-t border-border-subtle px-4 py-2">
          <p className="text-[11px] text-fg-muted">{data.caption}</p>
        </div>
      )}
    </Envelope>
  )
}
