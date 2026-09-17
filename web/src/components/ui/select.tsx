import type { SelectHTMLAttributes } from 'react'

export interface SelectOption {
  value: string
  label: string
}

export interface SelectProps extends SelectHTMLAttributes<HTMLSelectElement> {
  label?: string
  options: SelectOption[]
  placeholder?: string
}

export function Select({ label, options, placeholder, id, className, name, ...rest }: SelectProps) {
  const selectId = id ?? name
  return (
    <label className="flex flex-col gap-1" htmlFor={selectId}>
      {label ? <span className="text-sm font-medium text-neutral-300">{label}</span> : null}
      <select
        id={selectId}
        name={name}
        className={`w-full rounded-md border border-border bg-surface px-3 py-2 text-sm text-neutral-100 focus:border-brand focus:outline-none ${className ?? ''}`}
        {...rest}
      >
        {placeholder ? <option value="">{placeholder}</option> : null}
        {options.map((o) => (
          <option key={o.value} value={o.value}>
            {o.label}
          </option>
        ))}
      </select>
    </label>
  )
}