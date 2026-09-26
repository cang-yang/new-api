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

import { Field, FieldDescription, FieldLabel } from '@/components/ui/field'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'

export function RegexFailurePolicy(props: {
  value: unknown
  onChange: (value: 'passthrough' | 'error' | undefined) => void
  disabled?: boolean
  embedded?: boolean
}) {
  const { t } = useTranslation()
  const id = useId()
  const value =
    props.value === 'passthrough' || props.value === 'error'
      ? props.value
      : 'legacy'
  return (
    <Field>
      <FieldLabel htmlFor={id}>
        {props.embedded
          ? t('Embedded regex failure policy')
          : t('Regex failure policy')}
      </FieldLabel>
      <NativeSelect
        id={id}
        value={value}
        disabled={props.disabled}
        aria-describedby={`${id}-help`}
        onChange={(e) =>
          props.onChange(
            e.target.value === 'legacy'
              ? undefined
              : (e.target.value as 'passthrough' | 'error')
          )
        }
      >
        <NativeSelectOption value='legacy'>
          {t('Inherited / legacy behavior (strict)')}
        </NativeSelectOption>
        <NativeSelectOption value='passthrough'>
          {t('Keep original on failure (recommended for availability)')}
        </NativeSelectOption>
        <NativeSelectOption value='error'>
          {t('Return an error on failure')}
        </NativeSelectOption>
      </NativeSelect>
      <FieldDescription id={`${id}-help`}>
        {value === 'passthrough'
          ? t(
              'On processing errors or limits, retain the entire original response or request. Not suitable for privacy redaction.'
            )
          : t(
              'Processing errors stop the request. An upstream-generated response may be lost and may still be billed.'
            )}
      </FieldDescription>
      <FieldDescription>
        {t(
          'This policy handles processing failures only. No-match behavior is configured separately per rule.'
        )}
      </FieldDescription>
    </Field>
  )
}
