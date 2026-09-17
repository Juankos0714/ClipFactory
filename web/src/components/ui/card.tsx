import type { HTMLAttributes, ReactNode } from 'react'

export interface CardProps extends HTMLAttributes<HTMLElement> {
  children: ReactNode
}

export function Card({ children, className, ...rest }: CardProps) {
  return (
    <section className={`rounded-lg border border-border bg-surface-elevated ${className ?? ''}`} {...rest}>
      {children}
    </section>
  )
}

export interface CardHeaderProps {
  title: string
  action?: ReactNode
  id?: string
}

export function CardHeader({ title, action, id }: CardHeaderProps) {
  return (
    <div className="flex items-center justify-between gap-3 border-b border-border px-4 py-3">
      <h2 id={id} className="text-sm font-semibold text-neutral-200">
        {title}
      </h2>
      {action}
    </div>
  )
}