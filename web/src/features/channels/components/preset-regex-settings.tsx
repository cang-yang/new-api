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
import { useTranslation } from 'react-i18next'

import { EmptyState } from '@/components/empty-state'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Switch } from '@/components/ui/switch'

import { parsePresetEditor } from '../lib/sillytavern-editor'
import { RegexFailurePolicy } from './regex-failure-policy'

export function PresetRegexSettings(props: {
  config: Record<string, unknown>
  onChange: (value: string) => void
  disabled?: boolean
}) {
  const { t } = useTranslation()
  const parsed = parsePresetEditor(props.config)
  if (!parsed || parsed.regexScripts.length === 0) {
    return <EmptyState title={t('No embedded regex scripts')} />
  }
  return (
    <section
      className='flex flex-col gap-4'
      aria-label={t('Embedded regex scripts')}
    >
      <div className='flex flex-col gap-4'>
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
            <AlertTitle>{t('Some response rules generate HTML')}</AlertTitle>
            <AlertDescription className='text-amber-900 dark:text-amber-200'>
              {t(
                'Enabling these rules can replace the reply with HTML, CSS or JavaScript text in the API content. New API does not render it. Disable the marked rules if your client expects plain text.'
              )}
            </AlertDescription>
          </Alert>
        )}
      </div>
      <div className='divide-y'>
        {parsed.regexScripts.map((script) => (
          <div
            key={script.id || script.name}
            className='flex items-center gap-3 px-4 py-3'
          >
            <div className='min-w-0 flex-1 space-y-1'>
              <p className='text-sm font-medium break-words'>{script.name}</p>
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
  )
}
