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
import { useMutation } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { handleServerError } from '@/lib/handle-server-error'

import { previewWorldbook } from '../api'
import type { WorldbookConfig } from '../lib/worldbook'

export function WorldbookPreview(props: {
  config: WorldbookConfig
  presetValue?: string
  disabled?: boolean
}) {
  const { t } = useTranslation()
  const [input, setInput] = useState('')
  const key = `${JSON.stringify(props.config)}\0${props.presetValue ?? ''}\0${input}`
  const preview = useMutation({
    mutationFn: async (snapshot: {
      key: string
      config: WorldbookConfig
      presetValue?: string
      input: string
    }) => {
      let request: unknown = {
        messages: [{ role: 'user', content: snapshot.input }],
      }
      if (/^\s*[[{]/.test(snapshot.input)) {
        request = JSON.parse(snapshot.input)
        if (
          !request ||
          typeof request !== 'object' ||
          !('messages' in request) ||
          !Array.isArray(request.messages)
        ) {
          throw new Error(
            t('Paste plain text or a Chat Completions request with messages.')
          )
        }
      }
      let sillyTavernPreset: unknown
      if (snapshot.presetValue?.trim()) {
        try {
          sillyTavernPreset = JSON.parse(snapshot.presetValue)
        } catch {
          throw new Error(t('The channel preset is not valid JSON.'))
        }
      }
      return {
        key: snapshot.key,
        result: await previewWorldbook(
          snapshot.config,
          request,
          sillyTavernPreset
        ),
      }
    },
    onError: (error) => handleServerError(error),
    meta: { errorToast: false },
  })
  const result = preview.data?.key === key ? preview.data.result : undefined
  const reasons: Record<string, string> = {
    included: t('Included'),
    disabled: t('Disabled'),
    invalid: t('Invalid data'),
    no_match: t('No keyword match'),
    budget: t('Content limit reached'),
    duplicate: t('Duplicate content'),
  }
  return (
    <div className='space-y-4'>
      <p className='text-muted-foreground text-sm'>
        {t(
          'Uses the same matching rules as live requests. No model request or charge is made.'
        )}
      </p>
      <div className='space-y-2'>
        <Label htmlFor='worldbook-test-input'>
          {t('Conversation or request JSON')}
        </Label>
        <Textarea
          id='worldbook-test-input'
          rows={6}
          value={input}
          onChange={(event) => setInput(event.target.value)}
        />
      </div>
      <Button
        type='button'
        disabled={props.disabled || preview.isPending || !input.trim()}
        onClick={() =>
          preview.mutate({
            key,
            config: props.config,
            presetValue: props.presetValue,
            input,
          })
        }
      >
        {preview.isPending ? t('Testing...') : t('Run matching test')}
      </Button>
      {result && (
        <div className='space-y-4' aria-live='polite'>
          {[...new Set(result.warnings)].map((warning) => (
            <p key={warning} role='status' className='text-sm break-words'>
              {warning}
            </p>
          ))}
          {result.placement && (
            <div className='bg-muted/30 rounded-lg border p-3 text-sm'>
              <p className='font-medium'>
                {t('Insertion before the preset')}
              </p>
              <p className='text-muted-foreground mt-1 text-xs'>
                {t('Mode')}: {result.placement.mode} ·{' '}
                {t('Client message index')}: {result.placement.index} ·{' '}
                {t('Role')}:
                {' '}
                {result.placement.role}
                {result.placement.fallback
                  ? ` · ${t('Fallback applied')}`
                  : ''}
              </p>
            </div>
          )}
          <div className='divide-y rounded-lg border px-3'>
            {result.entries.map((entry) => (
              <div
                key={`${entry.book_id}:${entry.id}`}
                className='flex flex-wrap items-center justify-between gap-2 py-2 text-sm'
              >
                <span className='min-w-0 break-words'>
                  {entry.order ? `#${entry.order} ` : ''}
                  {entry.name}
                </span>
                <span className='text-muted-foreground text-xs'>
                  {reasons[entry.reason] || entry.reason}
                </span>
              </div>
            ))}
          </div>
          <div className='space-y-2'>
            <div className='flex items-center justify-between'>
              <h3 className='text-sm font-medium'>
                {t('Background to include')}
              </h3>
              <CopyButton value={result.text} tooltip={t('Copy')} size='sm' />
            </div>
            <pre className='bg-muted/30 max-h-80 overflow-y-auto rounded-lg border p-3 font-sans text-sm [overflow-wrap:anywhere] whitespace-pre-wrap'>
              {result.text || t('No background will be added.')}
            </pre>
          </div>
        </div>
      )}
    </div>
  )
}
