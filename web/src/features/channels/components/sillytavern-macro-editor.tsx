/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'

type Props = {
  config: Record<string, unknown>
  onChange: (value: string) => void
  disabled?: boolean
}

export function SillyTavernMacroEditor(props: Props) {
  const { t } = useTranslation()
  const [name, setName] = useState('')
  const values = (props.config.macro_values || {}) as Record<string, string>
  const normalizedName = name
    .trim()
    .replaceAll(/^\{\{|\}\}$/g, '')
    .trim()
  const nameExists = Object.hasOwn(values, normalizedName)

  return (
    <section
      className='bg-card space-y-4 rounded-xl border p-4'
      aria-label={t('Macro values and time')}
    >
      <div className='space-y-1'>
        <h4 className='text-sm font-semibold'>{t('Macro values and time')}</h4>
        <p className='text-muted-foreground text-xs leading-relaxed'>
          {t(
            'Provide literal values for unsupported macros. Built-in functions are evaluated by the gateway; these values are not executable code.'
          )}
        </p>
      </div>
      <label className='block space-y-2'>
        <span className='text-xs font-medium'>{t('Preset time zone')}</span>
        <Input
          placeholder='Asia/Shanghai'
          value={
            typeof props.config.time_zone === 'string'
              ? props.config.time_zone
              : ''
          }
          disabled={props.disabled}
          onChange={(event) =>
            props.onChange(
              JSON.stringify({ ...props.config, time_zone: event.target.value })
            )
          }
        />
      </label>
      {Object.entries(values).map(([key, value]) => (
        <div key={key} className='space-y-2 rounded-lg border p-3'>
          <div className='flex items-center justify-between gap-3'>
            <code className='text-xs break-all'>{`{{${key}}}`}</code>
            <Button
              type='button'
              variant='ghost'
              size='sm'
              disabled={props.disabled}
              aria-label={t('Remove macro {{name}}', { name: key })}
              onClick={() => {
                const next = { ...values }
                delete next[key]
                props.onChange(
                  JSON.stringify({ ...props.config, macro_values: next })
                )
              }}
            >
              {t('Remove')}
            </Button>
          </div>
          <Textarea
            aria-label={t('Value for macro {{name}}', { name: key })}
            value={value}
            disabled={props.disabled}
            className='min-h-20'
            onChange={(event) =>
              props.onChange(
                JSON.stringify({
                  ...props.config,
                  macro_values: { ...values, [key]: event.target.value },
                })
              )
            }
          />
        </div>
      ))}
      <div className='flex flex-col gap-2 sm:flex-row'>
        <Input
          aria-label={t('New macro name')}
          placeholder={t('New macro name')}
          value={name}
          disabled={props.disabled}
          onChange={(event) => setName(event.target.value)}
          aria-invalid={nameExists}
        />
        <Button
          type='button'
          variant='outline'
          disabled={props.disabled || !normalizedName || nameExists}
          onClick={() => {
            props.onChange(
              JSON.stringify({
                ...props.config,
                macro_values: { ...values, [normalizedName]: '' },
              })
            )
            setName('')
          }}
        >
          {t('Add macro value')}
        </Button>
      </div>
      {nameExists && (
        <p className='text-destructive text-xs' role='alert'>
          {t('This macro already has a value.')}
        </p>
      )}
    </section>
  )
}
