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
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import {
  Field,
  FieldDescription,
  FieldGroup,
  FieldLabel,
  FieldSet,
} from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'

type Props = {
  config: Record<string, unknown>
  onChange: (value: Record<string, unknown>) => void
  disabled?: boolean
}

export function SillyTavernMacroEditor(props: Props) {
  const { t } = useTranslation()
  const transportId = useId()
  const [name, setName] = useState('')
  const values = (props.config.macro_values || {}) as Record<string, string>
  const normalizedName = name
    .trim()
    .replaceAll(/^\{\{|\}\}$/g, '')
    .trim()
  const nameExists = Object.hasOwn(values, normalizedName)
  const toolText =
    props.config.tool_text &&
    typeof props.config.tool_text === 'object' &&
    !Array.isArray(props.config.tool_text)
      ? (props.config.tool_text as Record<string, unknown>)
      : {}
  const toolTextEnabled = toolText.enabled === true
  const updateToolText = (changes: Record<string, unknown>) =>
    props.onChange({
      ...props.config,
      tool_text: {
        enabled: toolTextEnabled,
        name: 'newapi_text',
        argument: 'display_stream',
        ...toolText,
        ...changes,
      },
    })

  return (
    <section
      className='flex flex-col gap-4'
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
      <Alert role='note'>
        <AlertTitle>{t('Built-in macros run automatically')}</AlertTitle>
        <AlertDescription className='space-y-2'>
          <p>
            {t(
              'Enabled preset entries automatically expand roll, random, text, time, history and request-local variable macros before sending. No separate switch is needed.'
            )}
          </p>
          <p>
            <code>{'{{roll 1999999}}'}</code>
            {' · '}
            <code>{'{{roll 1d99999}}'}</code>
            {' · '}
            <code>{'{{random::A::B}}'}</code>
          </p>
          <p>
            {t(
              'Macros that need browser state or persistent global writes are not emulated. Missing context is reported in the execution trace; literal values below can supply unavailable data.'
            )}
          </p>
        </AlertDescription>
      </Alert>
      <FieldSet className='rounded-lg border p-3' disabled={props.disabled}>
        <Field orientation='horizontal'>
          <div>
            <FieldLabel htmlFor={transportId}>
              {t('Enable native tool-text transport')}
            </FieldLabel>
            <FieldDescription>
              {t(
                'Adds one declarative function tool for providers that return the visible response in a JSON string. Browser scripts are preserved but never executed by New API.'
              )}
            </FieldDescription>
          </div>
          <Switch
            id={transportId}
            checked={toolTextEnabled}
            disabled={props.disabled}
            aria-label={t('Enable native tool-text transport')}
            onCheckedChange={(checked) => updateToolText({ enabled: checked })}
          />
        </Field>
        {toolTextEnabled && (
          <FieldGroup className='grid gap-3 sm:grid-cols-2'>
            <Field data-disabled={props.disabled}>
              <FieldLabel htmlFor={`${transportId}-name`}>
                {t('Transport tool name')}
              </FieldLabel>
              <Input
                id={`${transportId}-name`}
                value={typeof toolText.name === 'string' ? toolText.name : ''}
                disabled={props.disabled}
                onChange={(event) =>
                  updateToolText({ name: event.target.value })
                }
              />
            </Field>
            <Field data-disabled={props.disabled}>
              <FieldLabel htmlFor={`${transportId}-argument`}>
                {t('Transport text argument')}
              </FieldLabel>
              <Input
                id={`${transportId}-argument`}
                value={
                  typeof toolText.argument === 'string' ? toolText.argument : ''
                }
                disabled={props.disabled}
                onChange={(event) =>
                  updateToolText({ argument: event.target.value })
                }
              />
            </Field>
          </FieldGroup>
        )}
      </FieldSet>
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
            props.onChange({ ...props.config, time_zone: event.target.value })
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
                props.onChange({ ...props.config, macro_values: next })
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
              props.onChange({
                ...props.config,
                macro_values: { ...values, [key]: event.target.value },
              })
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
            props.onChange({
              ...props.config,
              macro_values: { ...values, [normalizedName]: '' },
            })
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
