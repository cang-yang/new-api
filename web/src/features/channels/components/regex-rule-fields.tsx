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

import {
  Accordion,
  AccordionItem,
  AccordionTrigger,
  AccordionContent,
} from '@/components/ui/accordion'
import { Field, FieldGroup, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'

import type { RegexRule } from '../lib/regex-rules'

export function RegexRuleFields(props: {
  rule: RegexRule
  onChange: (rule: RegexRule) => void
  disabled?: boolean
}) {
  const { t } = useTranslation()
  const id = useId()
  const rule = props.rule
  return (
    <FieldGroup className='gap-3'>
      <Field>
        <FieldLabel htmlFor={`${id}-name`}>{t('Rule name')}</FieldLabel>
        <Input
          id={`${id}-name`}
          disabled={props.disabled}
          value={rule.name || ''}
          onChange={(e) => props.onChange({ ...rule, name: e.target.value })}
        />
      </Field>
      <div className='grid gap-3 sm:grid-cols-2'>
        <Field>
          <FieldLabel htmlFor={`${id}-stage`}>{t('Direction')}</FieldLabel>
          <NativeSelect
            id={`${id}-stage`}
            disabled={props.disabled}
            value={rule.stage}
            onChange={(e) =>
              props.onChange({
                ...rule,
                stage: e.target.value as RegexRule['stage'],
              })
            }
          >
            <NativeSelectOption value='receive'>
              {t('Receive')}
            </NativeSelectOption>
            <NativeSelectOption value='send'>{t('Send')}</NativeSelectOption>
          </NativeSelect>
        </Field>
        <Field>
          <FieldLabel htmlFor={`${id}-action`}>{t('Action')}</FieldLabel>
          <NativeSelect
            id={`${id}-action`}
            disabled={props.disabled}
            value={rule.action}
            onChange={(e) =>
              props.onChange({
                ...rule,
                action: e.target.value as RegexRule['action'],
                replacement:
                  e.target.value === 'extract' && rule.replacement === ''
                    ? '$1'
                    : rule.replacement,
              })
            }
          >
            <NativeSelectOption value='replace'>
              {t('Replace / delete')}
            </NativeSelectOption>
            <NativeSelectOption value='extract'>
              {t('Extract captures')}
            </NativeSelectOption>
          </NativeSelect>
        </Field>
      </div>
      <Field>
        <FieldLabel htmlFor={`${id}-pattern`}>{t('Find pattern')}</FieldLabel>
        <Textarea
          id={`${id}-pattern`}
          disabled={props.disabled}
          value={rule.pattern}
          placeholder='/pattern/gimsu'
          onChange={(e) => props.onChange({ ...rule, pattern: e.target.value })}
        />
      </Field>
      <Field>
        <FieldLabel htmlFor={`${id}-replacement`}>
          {t('Replacement / capture template')}
        </FieldLabel>
        <Textarea
          id={`${id}-replacement`}
          disabled={props.disabled}
          value={rule.replacement}
          onChange={(e) =>
            props.onChange({ ...rule, replacement: e.target.value })
          }
        />
      </Field>
      <p className='text-muted-foreground text-xs'>
        {t(
          'Empty replacement deletes matches. Templates support $0, $1, $<name> and {{match}}. Extract keeps only matching captures; /g processes all matches.',
          { interpolation: { skipOnVariables: true } }
        )}
      </p>
      <Accordion>
        <AccordionItem value='scope'>
          <AccordionTrigger>
            {t('Rule scope and unmatched text')}
          </AccordionTrigger>
          <AccordionContent>
            <FieldGroup className='mt-3 gap-3'>
              {rule.stage === 'send' && (
                <Field>
                  <FieldLabel>{t('Message roles')}</FieldLabel>
                  <div className='flex flex-wrap gap-3'>
                    {(
                      ['user', 'assistant', 'system', 'developer'] as const
                    ).map((role) => (
                      <Field key={role} orientation='horizontal'>
                        <Switch
                          id={`${id}-${role}`}
                          disabled={props.disabled}
                          checked={(
                            rule.roles || ['user', 'assistant']
                          ).includes(role)}
                          onCheckedChange={(checked) =>
                            props.onChange({
                              ...rule,
                              roles: checked
                                ? [
                                    ...(rule.roles || ['user', 'assistant']),
                                    role,
                                  ]
                                : (rule.roles || ['user', 'assistant']).filter(
                                    (r) => r !== role
                                  ),
                            })
                          }
                        />
                        <FieldLabel htmlFor={`${id}-${role}`}>
                          {role}
                        </FieldLabel>
                      </Field>
                    ))}
                  </div>
                </Field>
              )}
              <div className='grid gap-3 sm:grid-cols-2'>
                {(['min_depth', 'max_depth'] as const).map((key) => (
                  <Field key={key}>
                    <FieldLabel htmlFor={`${id}-${key}`}>
                      {key === 'min_depth'
                        ? t('Minimum depth')
                        : t('Maximum depth')}
                    </FieldLabel>
                    <Input
                      id={`${id}-${key}`}
                      type='number'
                      min={0}
                      step={1}
                      disabled={props.disabled}
                      value={rule[key] ?? ''}
                      onChange={(e) =>
                        props.onChange({
                          ...rule,
                          [key]:
                            e.target.value === ''
                              ? undefined
                              : Number(e.target.value),
                        })
                      }
                    />
                  </Field>
                ))}
              </div>
              <Field>
                <FieldLabel htmlFor={`${id}-missing`}>
                  {t('When nothing matches')}
                </FieldLabel>
                <NativeSelect
                  id={`${id}-missing`}
                  disabled={props.disabled}
                  value={rule.missing_match || 'passthrough'}
                  onChange={(e) =>
                    props.onChange({
                      ...rule,
                      missing_match: e.target
                        .value as RegexRule['missing_match'],
                    })
                  }
                >
                  <NativeSelectOption value='passthrough'>
                    {t('Keep original text')}
                  </NativeSelectOption>
                  <NativeSelectOption value='empty'>
                    {t('Return empty text')}
                  </NativeSelectOption>
                </NativeSelect>
              </Field>
            </FieldGroup>
          </AccordionContent>
        </AccordionItem>
      </Accordion>
    </FieldGroup>
  )
}
