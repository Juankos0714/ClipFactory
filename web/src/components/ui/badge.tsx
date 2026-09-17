import type { HTMLAttributes, ReactNode } from 'react'
import { toneClasses, toneFor } from '@/lib/status'

export interface BadgeProps extends HTMLAttributes<HTMLSpanElement> {
  children: ReactNode
  /** Tono explícito; si no se pasa se infiere del texto del estado. */
  tone?: 'neutral' | 'green' | 'amber' | 'red' | 'blue' | 'purple'
}

export function Badge({ children, tone, className, ...rest }: BadgeProps) {
  const text = typeof children === 'string' ? children : ''
  const resolved = tone ?? toneFor(text.toLowerCase())
  return (
    <span
      className={`inline-flex items-center rounded-full border px-2 py-0.5 text-xs font-medium ${toneClasses(resolved)} ${className ?? ''}`}
      {...rest}
    >
      {children}
    </span>
  )
}

/** Badge cuyo texto es un estado (el tono se deriva automáticamente). */
export function StatusBadge({ status, className }: { status: string; className?: string }) {
  return (
    <Badge className={className} tone={toneFor(status.toLowerCase())}>
      {status}
    </Badge>
  )
}