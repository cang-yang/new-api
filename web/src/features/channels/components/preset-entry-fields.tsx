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
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Textarea } from '@/components/ui/textarea'

import { editPresetEntry, type PresetEntry } from '../lib/sillytavern-editor'

export function PresetEntryFields(props: {
  entry: PresetEntry
  value: string
  onChange: (value: string) => void
  disabled?: boolean
}) {
  const { t } = useTranslation()
  const id = useId()
  const update = (changes: Parameters<typeof editPresetEntry>[2]) =>
    props.onChange(
      editPresetEntry(props.value, props.entry.identifier, changes)
    )
  return (
    <FieldGroup className='gap-3'>
      <Field>
        <FieldLabel htmlFor={`${id}-name`}>{t('Entry name')}</FieldLabel>
        <Input
          id={`${id}-name`}
          value={props.entry.name}
          disabled={props.disabled}
          onChange={(event) => update({ name: event.target.value })}
        />
      </Field>
      {props.entry.marker ? (
        <p className='text-muted-foreground text-xs'>
          {t('This marker is filled from the request context.')}
        </p>
      ) : (
        <>
          <Field>
            <FieldLabel htmlFor={`${id}-role`}>{t('Message role')}</FieldLabel>
            <NativeSelect
              id={`${id}-role`}
              value={props.entry.role}
              disabled={props.disabled}
              onChange={(event) => update({ role: event.target.value })}
            >
              {['system', 'user', 'assistant'].map((role) => (
                <NativeSelectOption key={role} value={role}>
                  {role}
                </NativeSelectOption>
              ))}
            </NativeSelect>
          </Field>
          <Field>
            <FieldLabel htmlFor={`${id}-content`}>
              {t('Prompt content')}
            </FieldLabel>
            <Textarea
              id={`${id}-content`}
              className='min-h-48 font-mono text-xs'
              value={props.entry.content}
              disabled={props.disabled}
              onChange={(event) => update({ content: event.target.value })}
            />
          </Field>
          <Field>
            <FieldLabel htmlFor={`${id}-position`}>
              {t('Injection placement')}
            </FieldLabel>
            <NativeSelect
              id={`${id}-position`}
              value={props.entry.injection_position}
              disabled={props.disabled}
              onChange={(event) =>
                update({ injection_position: Number(event.target.value) })
              }
            >
              <NativeSelectOption value={0}>
                {t('In preset order')}
              </NativeSelectOption>
              <NativeSelectOption value={1}>
                {t('At chat depth')}
              </NativeSelectOption>
            </NativeSelect>
          </Field>
          {props.entry.injection_position === 1 && (
            <div className='grid gap-3 sm:grid-cols-2'>
              {(['injection_depth', 'injection_order'] as const).map((key) => (
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
              ))}
            </div>
          )}
        </>
      )}
    </FieldGroup>
  )
}
