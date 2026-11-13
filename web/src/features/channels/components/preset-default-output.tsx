import { useId } from 'react'
import { useTranslation } from 'react-i18next'

import {
  Field,
  FieldDescription,
  FieldLabel,
  FieldSet,
} from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'

export function PresetDefaultOutput(props: {
  config: Record<string, unknown>
  disabled?: boolean
  onChange: (config: Record<string, unknown>) => void
}) {
  const { t } = useTranslation()
  const id = useId()
  const value = props.config.default_output as
    | { format?: string; tag?: string }
    | undefined
  return (
    <FieldSet disabled={props.disabled}>
      <Field>
        <FieldLabel htmlFor={id}>{t('Default reply format')}</FieldLabel>
        <FieldDescription>
          {t(
            'Used only when the client does not specify a response format. Core preset scripts still run. An explicit client format takes priority.'
          )}
        </FieldDescription>
        <Select
          value={value?.format ?? 'text'}
          onValueChange={(format) => {
            if (!format) return
            props.onChange({
              ...props.config,
              default_output: {
                format,
                ...(format === 'tagged_text'
                  ? { tag: value?.tag ?? 'UpdateVariable' }
                  : {}),
              },
            })
          }}
        >
          <SelectTrigger id={id} className='w-full'>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value='text'>
              {t('Follow the task in the messages')}
            </SelectItem>
            <SelectItem value='json_object'>
              {t('A JSON object, without extra text')}
            </SelectItem>
            <SelectItem value='tagged_text'>
              {t('One named text block (for variables)')}
            </SelectItem>
          </SelectContent>
        </Select>
      </Field>
      {value?.format === 'tagged_text' && (
        <Field>
          <FieldLabel htmlFor={`${id}-tag`}>
            {t('Required block name')}
          </FieldLabel>
          <Input
            id={`${id}-tag`}
            value={value.tag ?? ''}
            maxLength={64}
            onChange={(event) => {
              props.onChange({
                ...props.config,
                default_output: {
                  format: 'tagged_text',
                  tag: event.target.value,
                },
              })
            }}
          />
          <FieldDescription>
            {t(
              'For example, UpdateVariable requires one complete <UpdateVariable>...</UpdateVariable> block. The gateway checks the wrapper, not the correctness of the variable changes.'
            )}
          </FieldDescription>
        </Field>
      )}
    </FieldSet>
  )
}
