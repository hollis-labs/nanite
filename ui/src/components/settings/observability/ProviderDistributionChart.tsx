import { Doughnut } from 'react-chartjs-2'
import type { ChartOptions } from 'chart.js'
import type { ExecutionMetrics } from '@/lib/types'

// Modern, desaturated palette — works on both light and dark backgrounds
const COLORS = [
  '#3b82f6', // blue-500
  '#8b5cf6', // violet-500
  '#06b6d4', // cyan-500
  '#f59e0b', // amber-500
  '#ec4899', // pink-500
  '#14b8a6', // teal-500
  '#6366f1', // indigo-500
  '#f97316', // orange-500
]

const options: ChartOptions<'doughnut'> = {
  responsive: true,
  maintainAspectRatio: false,
  cutout: '68%',
  plugins: {
    legend: {
      position: 'right',
      labels: {
        boxWidth: 8,
        boxHeight: 8,
        padding: 14,
        font: { size: 11, family: 'system-ui, sans-serif' },
        usePointStyle: true,
        pointStyle: 'circle',
        color: '#71717a',
      },
    },
    tooltip: {
      backgroundColor: '#18181b',
      borderColor: '#3f3f46',
      borderWidth: 1,
      cornerRadius: 8,
      padding: 10,
      bodyFont: { family: 'ui-monospace, SFMono-Regular, Menlo, monospace', size: 11 },
      titleFont: { family: 'system-ui, sans-serif', size: 12, weight: 'bold' as const },
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

  // Use CSS variable for border to adapt to theme
  const borderColor = getComputedStyle(document.documentElement)
    .getPropertyValue('--c-bg').trim() || '#ffffff'

  const chartData = {
    labels,
    datasets: [
      {
        data: values,
        backgroundColor: labels.map((_, i) => COLORS[i % COLORS.length]),
        borderColor,
        borderWidth: 2,
        hoverBorderWidth: 0,
      },
    ],
  }

  if (labels.length === 0) {
    return <div className="h-48 flex items-center justify-center text-xs text-fg-faint">No data</div>
  }

  return (
    <div className="h-48">
      <Doughnut data={chartData} options={options} />
    </div>
  )
}
