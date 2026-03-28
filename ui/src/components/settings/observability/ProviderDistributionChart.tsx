import { Doughnut } from 'react-chartjs-2'
import type { ChartOptions } from 'chart.js'
import type { ExecutionMetrics } from '@/lib/types'

const COLORS = [
  '#6366f1', // indigo
  '#8b5cf6', // violet
  '#06b6d4', // cyan
  '#10b981', // emerald
  '#f59e0b', // amber
  '#ef4444', // red
  '#ec4899', // pink
  '#14b8a6', // teal
]

const options: ChartOptions<'doughnut'> = {
  responsive: true,
  maintainAspectRatio: false,
  cutout: '65%',
  plugins: {
    legend: {
      position: 'right',
      labels: {
        boxWidth: 10,
        padding: 12,
        font: { size: 11 },
        usePointStyle: true,
        pointStyle: 'circle',
      },
    },
    tooltip: {
      backgroundColor: '#18181b',
      borderColor: '#3f3f46',
      borderWidth: 1,
      bodyFont: { family: 'ui-monospace, SFMono-Regular, Menlo, monospace', size: 11 },
    },
  },
}

interface ProviderDistributionChartProps {
  data: ExecutionMetrics[]
}

export function ProviderDistributionChart({ data }: ProviderDistributionChartProps) {
  const counts = new Map<string, number>()
  for (const m of data) {
    counts.set(m.provider, (counts.get(m.provider) ?? 0) + 1)
  }

  const labels = [...counts.keys()]
  const values = labels.map((l) => counts.get(l)!)

  const chartData = {
    labels,
    datasets: [
      {
        data: values,
        backgroundColor: labels.map((_, i) => COLORS[i % COLORS.length]),
        borderColor: '#09090b',
        borderWidth: 2,
      },
    ],
  }

  if (labels.length === 0) {
    return <div className="h-48 flex items-center justify-center text-xs text-zinc-600">No data</div>
  }

  return (
    <div className="h-48">
      <Doughnut data={chartData} options={options} />
    </div>
  )
}
