import { Line } from 'react-chartjs-2'
import type { ChartOptions } from 'chart.js'
import type { ExecutionMetrics } from '@/lib/types'

const options: ChartOptions<'line'> = {
  responsive: true,
  maintainAspectRatio: false,
  interaction: { mode: 'index', intersect: false },
  plugins: {
    legend: { display: false },
    tooltip: {
      backgroundColor: '#18181b',
      borderColor: '#3f3f46',
      borderWidth: 1,
      titleFont: { size: 11 },
      bodyFont: { family: 'ui-monospace, SFMono-Regular, Menlo, monospace', size: 11 },
      callbacks: {
        title: (items) => {
          const idx = items[0]?.dataIndex
          return idx !== undefined ? `Execution #${idx + 1}` : ''
        },
        afterBody: (items) => {
          const raw = items[0]?.raw as Record<string, unknown> | undefined
          if (!raw) return ''
          return `${raw.provider} · ${raw.model}`
        },
      },
    },
  },
  scales: {
    x: {
      display: true,
      grid: { display: false },
      ticks: { maxTicksLimit: 10, font: { size: 10 } },
    },
    y: {
      display: true,
      grid: { color: '#27272a' },
      ticks: {
        font: { size: 10 },
        callback: (value) => {
          const ms = Number(value)
          if (ms < 1000) return `${ms}ms`
          return `${(ms / 1000).toFixed(1)}s`
        },
      },
    },
  },
}

interface DurationChartProps {
  data: ExecutionMetrics[]
}

export function DurationChart({ data }: DurationChartProps) {
  const sorted = [...data].reverse()

  const chartData = {
    labels: sorted.map((m) => {
      const d = new Date(m.created_at)
      return `${d.getHours().toString().padStart(2, '0')}:${d.getMinutes().toString().padStart(2, '0')}`
    }),
    datasets: [
      {
        data: sorted.map((m) => ({
          x: 0,
          y: m.duration_ms,
          provider: m.provider,
          model: m.model,
        })),
        borderColor: '#6366f1',
        backgroundColor: 'rgba(99, 102, 241, 0.1)',
        borderWidth: 1.5,
        pointRadius: 2,
        pointHoverRadius: 4,
        tension: 0.3,
        fill: true,
      },
    ],
  }

  return (
    <div className="h-48">
      <Line data={chartData} options={options} />
    </div>
  )
}
