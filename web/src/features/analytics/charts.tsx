import type { ReactNode } from 'react'
import {
  Area,
  AreaChart,
  Bar,
  BarChart,
  CartesianGrid,
  Legend,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts'

const AXIS_TICK = { fill: '#a3a3a3', fontSize: 12 }
const TOOLTIP_STYLE = { background: '#0f1115', border: '1px solid #262a33', borderRadius: 8, fontSize: 12 }
const TOOLTIP_LABEL = { color: '#e6e8ec' }

const LEGEND_WRAPPER = { fontSize: 12 }

export function StackedBars({
  data,
  xKey,
  keys,
  labels,
  colors,
  ariaLabel,
  height = 260,
}: {
  data: Array<Record<string, number | string>>
  xKey: string
  keys: string[]
  labels: Record<string, string>
  colors: Record<string, string>
  ariaLabel: string
  height?: number
}) {
  return (
    <div role="img" aria-label={ariaLabel} className="h-full w-full">
      <ResponsiveContainer width="100%" height={height}>
        <BarChart data={data} margin={{ top: 8, right: 8, left: -16, bottom: 0 }}>
          <CartesianGrid stroke="#23272f" vertical={false} />
          <XAxis dataKey={xKey} tick={AXIS_TICK} axisLine={false} tickLine={false} />
          <YAxis tick={AXIS_TICK} axisLine={false} tickLine={false} allowDecimals={false} />
          <Tooltip contentStyle={TOOLTIP_STYLE} labelStyle={TOOLTIP_LABEL} cursor={{ fill: 'rgba(255,255,255,0.04)' }} />
          <Legend
            formatter={(value: string) => <span style={{ color: '#a3a3a3' }}>{value}</span>}
            wrapperStyle={LEGEND_WRAPPER}
          />
          {keys.map((k) => (
            <Bar key={k} dataKey={k} stackId="stack" fill={colors[k]} name={labels[k] ?? k} />
          ))}
        </BarChart>
      </ResponsiveContainer>
    </div>
  )
}

function shortDay(iso: string): string {
  const [, m, d] = iso.split('-')
  return `${d}/${m}`
}

function fullDay(label: ReactNode): ReactNode {
  if (typeof label !== 'string') return label
  const d = new Date(`${label}T00:00:00Z`)
  return Number.isNaN(d.getTime()) ? label : d.toLocaleDateString('es', { day: 'numeric', month: 'long', timeZone: 'UTC' })
}

export function PublishedArea({
  data,
  ariaLabel,
  height = 220,
}: {
  data: Array<{ day: string; published: number }>
  ariaLabel: string
  height?: number
}) {
  return (
    <div role="img" aria-label={ariaLabel} className="h-full w-full">
      <ResponsiveContainer width="100%" height={height}>
        <AreaChart data={data} margin={{ top: 8, right: 8, left: -16, bottom: 0 }}>
          <defs>
            <linearGradient id="brandFill" x1="0" y1="0" x2="0" y2="1">
              <stop offset="0%" stopColor="#ff3d00" stopOpacity={0.35} />
              <stop offset="100%" stopColor="#ff3d00" stopOpacity={0.02} />
            </linearGradient>
          </defs>
          <CartesianGrid stroke="#23272f" vertical={false} />
          <XAxis
            dataKey="day"
            tick={AXIS_TICK}
            axisLine={false}
            tickLine={false}
            tickFormatter={shortDay}
            tickMargin={6}
          />
          <YAxis tick={AXIS_TICK} axisLine={false} tickLine={false} allowDecimals={false} />
          <Tooltip
            contentStyle={TOOLTIP_STYLE}
            labelStyle={TOOLTIP_LABEL}
            labelFormatter={fullDay}
            cursor={{ stroke: '#3f4753', strokeDasharray: '4 4' }}
          />
          <Area
            type="monotone"
            dataKey="published"
            name="Publicadas"
            stroke="#ff3d00"
            strokeWidth={2}
            fill="url(#brandFill)"
          />
        </AreaChart>
      </ResponsiveContainer>
    </div>
  )
}