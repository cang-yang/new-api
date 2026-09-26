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
import { useEffect, useId, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { EmptyState } from '@/components/empty-state'
import { JsonCodeEditor } from '@/components/json-code-editor'
import {
  Accordion,
  AccordionItem,
  AccordionTrigger,
  AccordionContent,
} from '@/components/ui/accordion'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Field, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'

import { normalizeResponseTextFilter } from '../lib/channel-form'
import {
  editableRegexRulesSchema,
  importRegexScripts,
  type RegexRule,
  type RegexRulesConfig,
} from '../lib/regex-rules'
import { RegexFailurePolicy } from './regex-failure-policy'
import { RegexRuleFields } from './regex-rule-fields'

export function RegexRulesEditor(props: {
  value: string
  onChange: (value: string) => void
  disabled?: boolean
}) {
  const { t } = useTranslation()
  const id = useId()
  const [warning, setWarning] = useState('')
  const [importing, setImporting] = useState(false)
  const latestValue = useRef(props.value)
  latestValue.current = props.value
  const mounted = useRef(true)
  useEffect(() => {
    mounted.current = true
    return () => {
      mounted.current = false
    }
  }, [])
  const disabled = props.disabled || importing
  let parsed: Record<string, unknown> | null = null
  try {
    const value = JSON.parse(props.value)
    if (value && typeof value === 'object' && !Array.isArray(value)) {
      parsed = value
    }
  } catch {
    /* Advanced JSON remains editable. */
  }
  const result = editableRegexRulesSchema.safeParse(parsed)
  let config: RegexRulesConfig | null = null
  if (!props.value.trim()) {
    config = {
      mode: 'rules',
      enable_send: false,
      failure_policy: 'passthrough',
      rules: [],
    }
  } else if (result.success) config = result.data
  const save = (next: RegexRulesConfig) =>
    props.onChange(JSON.stringify(next, null, 2))
  const update = (index: number, rule: RegexRule) => {
    if (config) {
      save({
        ...config,
        rules: config.rules.map((r, i) => (i === index ? rule : r)),
      })
    }
  }
  return (
    <div className='space-y-4'>
      {(config || parsed) && (
        <RegexFailurePolicy
          value={(config || parsed)?.failure_policy}
          disabled={disabled}
          onChange={(policy) => {
            const next = { ...(config || parsed) }
            if (policy === undefined) delete next.failure_policy
            else next.failure_policy = policy
            props.onChange(JSON.stringify(next, null, 2))
          }}
        />
      )}
      {config ? (
        <>
          <Field orientation='horizontal'>
            <Switch
              id={`${id}-send`}
              disabled={disabled}
              checked={config.enable_send === true}
              onCheckedChange={(checked) =>
                save({ ...config, enable_send: checked })
              }
            />
            <FieldLabel htmlFor={`${id}-send`}>
              {t('Enable send-side regex')}
            </FieldLabel>
          </Field>
          <p className='text-muted-foreground text-xs'>
            {t(
              'Rules run from top to bottom. Send rules change outgoing message text and do not buffer responses.'
            )}{' '}
            {t(
              'Channel rules run first, followed by embedded preset rules. Send rules process incoming messages before preset assembly.'
            )}
          </p>
          {config.rules.some(
            (rule) => rule.stage === 'receive' && !rule.disabled
          ) && (
            <Alert>
              <AlertDescription>
                {t('Applicable receive rules buffer streaming responses.')}
              </AlertDescription>
            </Alert>
          )}
          <Field>
            <FieldLabel htmlFor={`${id}-models`}>
              {t('Models (comma-separated, empty for all)')}
            </FieldLabel>
            <Input
              id={`${id}-models`}
              disabled={disabled}
              value={(config.models || []).join(',')}
              placeholder={t('Comma-separated model names')}
              onChange={(e) =>
                save({
                  ...config,
                  models:
                    e.target.value === '' ? [] : e.target.value.split(','),
                })
              }
              onBlur={(e) =>
                save({
                  ...config,
                  models: e.target.value
                    .split(',')
                    .map((model) => model.trim())
                    .filter(Boolean),
                })
              }
            />
          </Field>
          {config.rules.length === 0 && (
            <EmptyState title={t('No regex rules')} className='min-h-24' />
          )}
          {config.rules.map((rule, index) => (
            <section
              key={rule.id}
              className='space-y-3 rounded-xl border p-4'
              aria-label={t('Regex rule {{number}}', { number: index + 1 })}
            >
              <div className='flex flex-wrap items-center gap-2'>
                <span className='mr-auto text-sm font-semibold'>
                  {index + 1}. {rule.name || t('Regex rule')}
                </span>
                <Switch
                  aria-label={t('Enable rule')}
                  checked={!rule.disabled}
                  disabled={disabled}
                  onCheckedChange={(checked) =>
                    update(index, { ...rule, disabled: !checked })
                  }
                />
                {([-1, 1] as const).map((offset) => (
                  <Button
                    key={offset}
                    type='button'
                    variant='outline'
                    size='sm'
                    disabled={
                      disabled ||
                      index + offset < 0 ||
                      index + offset >= config.rules.length
                    }
                    onClick={() => {
                      const rules = [...config.rules]
                      ;[rules[index], rules[index + offset]] = [
                        rules[index + offset],
                        rules[index],
                      ]
                      save({ ...config, rules })
                    }}
                  >
                    {offset === -1 ? t('Move rule up') : t('Move rule down')}
                  </Button>
                ))}
                <Button
                  type='button'
                  variant='ghost'
                  size='sm'
                  disabled={disabled}
                  onClick={() =>
                    save({
                      ...config,
                      rules: config.rules.filter((_, i) => i !== index),
                    })
                  }
                >
                  {t('Remove rule')}
                </Button>
              </div>
              <RegexRuleFields
                rule={rule}
                disabled={disabled}
                onChange={(next) => update(index, next)}
              />
            </section>
          ))}
          <Button
            type='button'
            variant='outline'
            disabled={disabled}
            onClick={() =>
              save({
                ...config,
                rules: [
                  ...config.rules,
                  {
                    id: crypto.randomUUID(),
                    stage: 'receive',
                    action: 'replace',
                    pattern: '',
                    replacement: '',
                  },
                ],
              })
            }
          >
            {t('Add regex rule')}
          </Button>
          <Field>
            <FieldLabel htmlFor={`${id}-import`}>
              {t('Import SillyTavern regex JSON')}
            </FieldLabel>
            <Input
              id={`${id}-import`}
              type='file'
              accept='.json,application/json'
              disabled={disabled}
              onChange={async (e) => {
                const file = e.target.files?.[0]
                e.target.value = ''
                if (!file) return
                if (file.size > 6 * 1024 * 1024) {
                  setWarning(t('Regex import must be 6 MiB or smaller.'))
                  return
                }
                const startingValue = props.value
                setImporting(true)
                try {
                  const imported = importRegexScripts(
                    JSON.parse(await file.text())
                  )
                  if (!mounted.current) return
                  if (latestValue.current !== startingValue) {
                    setWarning(
                      t(
                        'Configuration changed while importing. Import again to keep the latest edits.'
                      )
                    )
                    return
                  }
                  const next = {
                    ...config,
                    rules: [...config.rules, ...imported.rules],
                    imported_scripts: [
                      ...(Array.isArray(config.imported_scripts)
                        ? config.imported_scripts
                        : []),
                      ...imported.originals,
                    ],
                  }
                  if (!editableRegexRulesSchema.safeParse(next).success) {
                    setWarning(
                      t('Regex import exceeds rule count or text size limits.')
                    )
                    return
                  }
                  save(next)
                  setWarning(
                    imported.warnings.length
                      ? t(
                          'Unsupported placements, macros or trim strings were retained but disabled: {{names}}',
                          { names: imported.warnings.join(', ') }
                        )
                      : ''
                  )
                } catch {
                  if (mounted.current) {
                    setWarning(t('Invalid SillyTavern regex script'))
                  }
                } finally {
                  if (mounted.current) setImporting(false)
                }
              }}
            />
          </Field>
          {warning && (
            <Alert variant='destructive'>
              <AlertDescription>{warning}</AlertDescription>
            </Alert>
          )}
        </>
      ) : (
        <Alert>
          <AlertDescription>
            {t(
              'Legacy or advanced configuration is preserved. Convert explicitly to edit ordered rules.'
            )}
          </AlertDescription>
        </Alert>
      )}
      {!config &&
        (parsed?.mode === 'regex_extract' ||
          parsed?.mode === 'tag_extract') && (
          <Button
            type='button'
            disabled={disabled}
            variant='outline'
            onClick={() => {
              const legacy = normalizeResponseTextFilter(parsed) as Record<
                string,
                unknown
              >
              save({
                mode: 'rules',
                enable_send: false,
                ...(legacy.failure_policy === 'error' ||
                legacy.failure_policy === 'passthrough'
                  ? { failure_policy: legacy.failure_policy }
                  : {}),
                ...(Array.isArray(legacy.models)
                  ? { models: legacy.models as string[] }
                  : {}),
                rules: [
                  {
                    id: crypto.randomUUID(),
                    stage: 'receive',
                    action: 'extract',
                    pattern: String(legacy.pattern || ''),
                    replacement: '$1',
                    missing_match:
                      legacy.missing_match === 'empty'
                        ? 'empty'
                        : 'passthrough',
                    trim_capture: legacy.trim_capture === true,
                  },
                ],
              })
            }}
          >
            {t('Convert legacy filter to rules')}
          </Button>
        )}
      <Accordion
        key={config ? 'rules' : 'advanced'}
        defaultValue={config ? [] : ['advanced']}
      >
        <AccordionItem value='advanced'>
          <AccordionTrigger>{t('Advanced regex JSON')}</AccordionTrigger>
          <AccordionContent>
            <JsonCodeEditor
              value={props.value}
              onChange={props.onChange}
              disabled={disabled}
              ariaLabel={t('Response Text Filter')}
              className='mt-3'
            />
          </AccordionContent>
        </AccordionItem>
      </Accordion>
    </div>
  )
}
