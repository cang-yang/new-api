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
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { EmptyState } from '@/components/empty-state'
import {
  Accordion,
  AccordionContent,
  AccordionItem,
  AccordionTrigger,
} from '@/components/ui/accordion'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'

import {
  parsePresetEditor,
  updatePresetEntries,
} from '../lib/sillytavern-editor'
import { RegexFailurePolicy } from './regex-failure-policy'
import { SillyTavernMacroEditor } from './sillytavern-macro-editor'

type Props = {
  value: string
  onChange: (value: string) => void
  disabled?: boolean
}

export function SillyTavernPresetEditor(props: Props) {
  const { t } = useTranslation()
  const [search, setSearch] = useState('')
  const parsed = useMemo(() => parsePresetEditor(props.value), [props.value])
  if (!parsed) return null
  const query = search.trim().toLocaleLowerCase()
  const entries = parsed.entries.filter((entry) =>
    `${entry.name} ${entry.identifier}`.toLocaleLowerCase().includes(query)
  )
  const enabledCount = parsed.entries.filter((entry) => entry.enabled).length

  return (
    <div className='space-y-4'>
      <section
        className='bg-card overflow-hidden rounded-xl border'
        aria-label={t('Preset entries')}
      >
        <div className='bg-muted/30 space-y-3 border-b p-4'>
          <div className='flex flex-wrap items-center justify-between gap-2'>
            <div className='flex items-center gap-2'>
              <h4 className='text-sm font-semibold'>{t('Preset entries')}</h4>
              <Badge variant='secondary'>
                {enabledCount} / {parsed.entries.length}
              </Badge>
            </div>
            <Button
              type='button'
              variant='ghost'
              size='sm'
              disabled={props.disabled || !parsed.config.entry_overrides}
              onClick={() => {
                const config = { ...parsed.config }
                delete config.entry_overrides
                props.onChange(JSON.stringify(config))
              }}
            >
              {t('Restore preset defaults')}
            </Button>
          </div>
          <p className='text-muted-foreground text-xs leading-relaxed'>
            {t(
              'Entries follow the preset order. Switches override this channel only; the imported preset stays intact.'
            )}
          </p>
          <Input
            value={search}
            onChange={(event) => setSearch(event.target.value)}
            aria-label={t('Search preset entries')}
            placeholder={t('Search preset entries')}
          />
          <div className='flex flex-wrap gap-2'>
            <Button
              type='button'
              size='sm'
              variant='outline'
              disabled={props.disabled || entries.length === 0}
              onClick={() =>
                props.onChange(
                  updatePresetEntries(
                    props.value,
                    entries.map((entry) => entry.identifier),
                    true
                  )
                )
              }
            >
              {t('Enable visible entries')}
            </Button>
            <Button
              type='button'
              size='sm'
              variant='outline'
              disabled={props.disabled || entries.length === 0}
              onClick={() =>
                props.onChange(
                  updatePresetEntries(
                    props.value,
                    entries.map((entry) => entry.identifier),
                    false
                  )
                )
              }
            >
              {t('Disable visible entries')}
            </Button>
          </div>
        </div>
        <div className='max-h-[28rem] overflow-y-auto overscroll-contain'>
          {entries.length === 0 ? (
            <EmptyState
              className='min-h-36'
              title={t('No matching preset entries')}
            />
          ) : (
            <Accordion multiple>
              {entries.map((entry, index) => (
                <AccordionItem
                  key={entry.identifier}
                  value={entry.identifier}
                  className='px-4'
                >
                  <div className='flex items-center gap-3'>
                    <span className='text-muted-foreground w-5 shrink-0 text-right font-mono text-xs'>
                      {index + 1}
                    </span>
                    <AccordionTrigger className='min-w-0 flex-1 py-3'>
                      <span className='min-w-0 space-y-1'>
                        <span className='block break-words'>{entry.name}</span>
                        <span className='text-muted-foreground flex flex-wrap gap-1.5 text-xs font-normal'>
                          <span>{entry.role}</span>
                          {entry.marker && <span>· {t('Context marker')}</span>}
                          {entry.enabled !== entry.originalEnabled && (
                            <span className='text-primary'>
                              · {t('Modified')}
                            </span>
                          )}
                        </span>
                      </span>
                    </AccordionTrigger>
                    <Switch
                      checked={entry.enabled}
                      disabled={props.disabled}
                      aria-label={t('Enable preset entry {{name}}', {
                        name: entry.name,
                      })}
                      onCheckedChange={(checked) =>
                        props.onChange(
                          updatePresetEntries(
                            props.value,
                            [entry.identifier],
                            checked
                          )
                        )
                      }
                    />
                  </div>
                  <AccordionContent>
                    <div className='space-y-2 pb-3 pl-8'>
                      <code className='text-muted-foreground text-xs break-all'>
                        {entry.identifier}
                      </code>
                      {entry.content ? (
                        <pre className='bg-muted/30 max-h-80 overflow-auto rounded-lg border p-3 font-mono text-xs leading-relaxed break-words whitespace-pre-wrap'>
                          {entry.content}
                        </pre>
                      ) : (
                        <p className='text-muted-foreground text-xs'>
                          {t('This marker is filled from the request context.')}
                        </p>
                      )}
                    </div>
                  </AccordionContent>
                </AccordionItem>
              ))}
            </Accordion>
          )}
        </div>
      </section>
      {parsed.regexScripts.length > 0 && (
        <section
          className='bg-card overflow-hidden rounded-xl border'
          aria-label={t('Embedded regex scripts')}
        >
          <div className='bg-muted/30 space-y-2 border-b p-4'>
            <div className='flex items-center justify-between gap-3'>
              <div className='flex items-center gap-2'>
                <h4 className='text-sm font-semibold'>
                  {t('Embedded regex scripts')}
                </h4>
                <Badge variant='secondary'>{parsed.regexScripts.length}</Badge>
              </div>
              <Switch
                checked={parsed.config.enable_embedded_regex === true}
                disabled={props.disabled}
                aria-label={t('Enable embedded regex scripts')}
                onCheckedChange={(checked) =>
                  props.onChange(
                    JSON.stringify({
                      ...parsed.config,
                      enable_embedded_regex: checked,
                    })
                  )
                }
              />
            </div>
            <div className='flex items-center justify-between gap-3'>
              <span className='text-sm'>
                {t('Enable embedded send-side regex')}
              </span>
              <Switch
                checked={parsed.config.enable_send_regex === true}
                disabled={props.disabled}
                aria-label={t('Enable embedded send-side regex')}
                onCheckedChange={(checked) =>
                  props.onChange(
                    JSON.stringify({
                      ...parsed.config,
                      enable_send_regex: checked,
                    })
                  )
                }
              />
            </div>
            <p className='text-muted-foreground text-xs leading-relaxed'>
              {t(
                'Receive scripts process upstream content before it reaches the client. Only applicable enabled receive scripts buffer streaming responses. Send scripts are off by default and do not buffer responses.'
              )}
            </p>
            <p className='text-muted-foreground text-xs'>
              {t(
                'Imported scripts are available but both receive and send processing require explicit enablement.'
              )}
            </p>
            <RegexFailurePolicy
              embedded
              value={parsed.config.regex_failure_policy}
              disabled={props.disabled}
              onChange={(policy) => {
                const next = { ...parsed.config }
                if (policy === undefined) delete next.regex_failure_policy
                else next.regex_failure_policy = policy
                props.onChange(JSON.stringify(next))
              }}
            />
            {parsed.regexScripts.some(
              (script) => script.responseSide && script.generatesHtml
            ) && (
              <Alert className='border-amber-500/40 bg-amber-50 text-amber-950 dark:bg-amber-950/30 dark:text-amber-100'>
                <AlertTitle>
                  {t('Some response rules generate HTML')}
                </AlertTitle>
                <AlertDescription className='text-amber-900 dark:text-amber-200'>
                  {t(
                    'Enabling these rules can replace the reply with HTML, CSS or JavaScript text in the API content. New API does not render it. Disable the marked rules if your client expects plain text.'
                  )}
                </AlertDescription>
              </Alert>
            )}
          </div>
          <div className='max-h-80 divide-y overflow-y-auto'>
            {parsed.regexScripts.map((script) => (
              <div
                key={script.id || script.name}
                className='flex items-center gap-3 px-4 py-3'
              >
                <div className='min-w-0 flex-1 space-y-1'>
                  <p className='text-sm font-medium break-words'>
                    {script.name}
                  </p>
                  {script.responseSide && script.generatesHtml && (
                    <Badge
                      variant='outline'
                      className='border-amber-500/40 text-amber-800 dark:text-amber-200'
                    >
                      {t('May generate HTML')}
                    </Badge>
                  )}
                  <p className='text-muted-foreground text-xs'>
                    {script.sendSide && <span>{t('Send-side')} · </span>}
                    {script.responseSide
                      ? t(
                          'Response-side · incompatible syntax is skipped with a warning'
                        )
                      : !script.sendSide &&
                        t('Unsupported placement · preserved only')}
                  </p>
                </div>
                <Switch
                  checked={script.enabled}
                  disabled={
                    props.disabled ||
                    !(
                      (parsed.config.enable_embedded_regex &&
                        script.responseSide) ||
                      (parsed.config.enable_send_regex && script.sendSide)
                    ) ||
                    !script.id ||
                    !(script.responseSide || script.sendSide)
                  }
                  aria-label={t('Enable regex script {{name}}', {
                    name: script.name,
                  })}
                  onCheckedChange={(checked) =>
                    props.onChange(
                      JSON.stringify({
                        ...parsed.config,
                        regex_overrides: {
                          ...((parsed.config.regex_overrides || {}) as Record<
                            string,
                            boolean
                          >),
                          [script.id]: checked,
                        },
                      })
                    )
                  }
                />
              </div>
            ))}
          </div>
        </section>
      )}
      <SillyTavernMacroEditor
        config={parsed.config}
        onChange={props.onChange}
        disabled={props.disabled}
      />
    </div>
  )
}
