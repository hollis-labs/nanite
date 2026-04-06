import { useState } from 'react'
import { ArrowUp, ArrowDown } from 'lucide-react'

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
    <div className="rounded-sm border border-border-subtle bg-bg-elevated/50 overflow-hidden">
      {data.title && (
        <div className="px-4 py-3 border-b border-border">
          <h4 className="text-sm font-medium text-fg">{data.title}</h4>
        </div>
      )}
      <div className="overflow-x-auto">
        <table className="w-full text-sm">
          <thead>
            <tr className="border-b border-border/50">
              {data.columns.map((col) => (
                <th
                  key={col.key}
                  className="px-4 py-2 text-left text-xs font-medium text-fg-muted"
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
                      className="inline-flex w-full items-center gap-1 text-left select-none hover:text-fg-secondary"
                      onClick={() => handleSort(col.key)}
                    >
                      <span>{col.label}</span>
                      {sortKey === col.key && (
                        sortAsc
                          ? <ArrowUp className="w-3 h-3" />
                          : <ArrowDown className="w-3 h-3" />
                      )}
                    </button>
                  ) : (
                    <span className="inline-flex items-center gap-1">
                      {col.label}
                    </span>
                  )}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {sortedRows.map((row, ri) => (
              <tr key={`row-${ri}`} className="border-b border-border/30 last:border-0">
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
        <div className="px-4 py-2 border-t border-border/50">
          <p className="text-xs text-fg-muted">{data.caption}</p>
        </div>
      )}
    </div>
  )
}
