/** Mapa determinista estado → tono visual (sin "IA"; reglas en el contrato). */

export type Tone = 'neutral' | 'green' | 'amber' | 'red' | 'blue' | 'purple'

const TONE_CLASSES: Record<Tone, string> = {
  neutral: 'border-border bg-surface-elevated text-neutral-300',
  green: 'border-emerald-500/30 bg-emerald-500/10 text-emerald-400',
  amber: 'border-amber-500/30 bg-amber-500/10 text-amber-400',
  red: 'border-red-500/30 bg-red-500/10 text-red-400',
  blue: 'border-sky-500/30 bg-sky-500/10 text-sky-400',
  purple: 'border-violet-500/30 bg-violet-500/10 text-violet-400',
}

/** Estados "progresando" → amber; finalizados → green; fallidos → red. */
const STATE_TONE: Record<string, Tone> = {
  detected: 'blue',
  downloaded: 'neutral',
  processing: 'amber',
  incoming: 'blue',
  queued: 'blue',
  running: 'amber',
  done: 'green',
  completed: 'green',
  published: 'green',
  pending: 'blue',
  waiting_rate_limit: 'purple',
  skipped: 'neutral',
  error: 'red',
  failed: 'red',
  active: 'green',
  inactive: 'neutral',
  starting: 'amber',
  stopping: 'amber',
  ok: 'green',
  degraded: 'amber',
  stopped: 'neutral',
}

export function toneFor(status: string): Tone {
  return STATE_TONE[status] ?? 'neutral'
}

export function toneClasses(tone: Tone): string {
  return TONE_CLASSES[tone]
}