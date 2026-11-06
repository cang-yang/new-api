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
import { useId } from 'react'
import { useTranslation } from 'react-i18next'

import { Field, FieldGroup, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Textarea } from '@/components/ui/textarea'

import type { PresetEntry, PresetEntryChanges } from '../lib/sillytavern-editor'

export function PresetEntryFields(props: {
  entry: PresetEntry
  onChange: (changes: PresetEntryChanges) => void
  disabled?: boolean
}) {
  const { t } = useTranslation()
  const id = useId()
  const update = props.onChange
  return (
    <FieldGroup className='min-h-0 flex-1 gap-4'>
      <div className='grid shrink-0 gap-3 sm:grid-cols-2'>
        <Field data-disabled={props.disabled}>
          <FieldLabel htmlFor={`${id}-name`}>{t('Entry name')}</FieldLabel>
          <Input
            id={`${id}-name`}
            value={props.entry.name}
            disabled={props.disabled}
            onChange={(event) => update({ name: event.target.value })}
          />
        </Field>
        {!props.entry.marker && (
          <Field data-disabled={props.disabled}>
            <FieldLabel htmlFor={`${id}-role`}>{t('Message role')}</FieldLabel>
            <Select
              value={props.entry.role}
              disabled={props.disabled}
              onValueChange={(value) => {
                if (value) update({ role: value })
              }}
              items={['system', 'user', 'assistant'].map((role) => ({
                value: role,
                label: role,
              }))}
            >
              <SelectTrigger id={`${id}-role`} className='w-full'>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {['system', 'user', 'assistant'].map((role) => (
                  <SelectItem key={role} value={role}>
                    {role}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </Field>
        )}
      </div>
      {props.entry.marker ? (
        <p className='text-muted-foreground rounded-lg border border-dashed p-6 text-sm leading-relaxed'>
          {t('This marker is filled from the request context.')}
        </p>
      ) : (
        <>
          <Field data-disabled={props.disabled} className='shrink-0'>
            <FieldLabel htmlFor={`${id}-position`}>
              {t('Injection placement')}
            </FieldLabel>
            <Select
              value={String(props.entry.injection_position)}
              disabled={props.disabled}
              onValueChange={(value) =>
                update({ injection_position: Number(value) })
              }
              items={[
                { value: '0', label: t('In preset order') },
                { value: '1', label: t('At chat depth') },
              ]}
            >
              <SelectTrigger id={`${id}-position`} className='w-full'>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value='0'>{t('In preset order')}</SelectItem>
                <SelectItem value='1'>{t('At chat depth')}</SelectItem>
              </SelectContent>
            </Select>
          </Field>
          {props.entry.injection_position === 1 && (
            <div className='flex shrink-0 flex-col gap-2'>
              <div className='grid gap-3 sm:grid-cols-2'>
                {(['injection_depth', 'injection_order'] as const).map(
                  (key) => (
                    <Field key={key}>
                      <FieldLabel htmlFor={`${id}-${key}`}>
                        {key === 'injection_depth'
                          ? t('Chat depth')
                          : t('Injection priority')}
                      </FieldLabel>
                      <Input
                        id={`${id}-${key}`}
                        type='number'
                        min={0}
                        max={key === 'injection_depth' ? 1000 : undefined}
                        step={1}
                        value={props.entry[key]}
                        disabled={props.disabled}
                        onChange={(event) => {
                          const number = Number(event.target.value)
                          if (
                            Number.isSafeInteger(number) &&
                            number >= 0 &&
                            (key !== 'injection_depth' || number <= 1000)
                          ) {
                            update({ [key]: number })
                          }
                        }}
                      />
                    </Field>
                  )
                )}
              </div>
              <p className='text-muted-foreground text-xs leading-relaxed'>
                {t(
                  'Chat depth controls injection into the conversation; list position does not.'
                )}
              </p>
            </div>
          )}
          <Field data-disabled={props.disabled} className='min-h-64 flex-1'>
            <FieldLabel htmlFor={`${id}-content`}>
              {t('Prompt content')}
            </FieldLabel>
            <Textarea
              id={`${id}-content`}
              className='[field-sizing:fixed] max-h-none min-h-56 flex-1 resize-none text-base leading-relaxed break-words whitespace-pre-wrap md:text-base'
              wrap='soft'
              value={props.entry.content}
              disabled={props.disabled}
              onChange={(event) => update({ content: event.target.value })}
            />
          </Field>
        </>
      )}
    </FieldGroup>
  )
}
