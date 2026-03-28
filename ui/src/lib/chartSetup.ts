import {
  Chart as ChartJS,
  CategoryScale,
  LinearScale,
  PointElement,
  LineElement,
  ArcElement,
  BarElement,
  Tooltip,
  Legend,
  Filler,
} from 'chart.js'

ChartJS.register(
  CategoryScale,
  LinearScale,
  PointElement,
  LineElement,
  ArcElement,
  BarElement,
  Tooltip,
  Legend,
  Filler,
)

ChartJS.defaults.color = '#a1a1aa'
ChartJS.defaults.borderColor = '#27272a'
ChartJS.defaults.font.family = 'ui-monospace, SFMono-Regular, Menlo, monospace'
ChartJS.defaults.font.size = 11
