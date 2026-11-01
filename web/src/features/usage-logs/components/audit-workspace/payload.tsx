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

import { CopyButton } from '@/components/copy-button'
import { EmptyState } from '@/components/empty-state'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'

import {
  displayValue,
  formatCapture,
  requestDocuments,
  responseDocuments,
  type AuditCapture,
} from '../../lib/audit-workspace'
import { parseBodyAuditResponse } from '../../lib/body-audit-response'
import { DocumentBrowser } from './document-browser'
import { ReadingViewport, TextBlock } from './reader'

export function AuditPayload(props: {
  capture: AuditCapture
  requestPath?: string
  final?: boolean
  notice?: string
  emptyMessage?: string
}) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const [view, setView] = useState('readable')
  const parsed = useMemo(
    () =>
      parseBodyAuditResponse({
        body: props.capture.body,
        encoding: props.capture.encoding,
        contentType: props.capture.contentType,
        requestPath: props.requestPath,
      }),
    [props.capture, props.requestPath]
  )
  const documents = useMemo(() => {
    if (props.capture.request || props.capture.derived) {
      try {
        return requestDocumentsForCapture(props.capture)
      } catch {
        return [
          {
            id: 'raw',
            category: 'data' as const,
            value: props.capture.body,
            invalid: true,
          },
        ]
      }
    }
    return responseDocuments(parsed)
  }, [parsed, props.capture])
  const formatted = useMemo(() => {
    if (props.final && documents.length) {
      return displayValue(
        documents.map((doc) => ({
          type: doc.category,
          name: doc.name,
          choice: doc.ordinal,
          value: doc.value,
          metadata: doc.metadata,
        }))
      )
    }
    return formatCapture(props.capture.body)
  }, [documents, props.capture.body, props.final])
  const warningLabels = {
    length: t('Generation stopped at the output limit'),
    'upstream-error': t('Upstream reported a response error'),
    'malformed-event': t('Some stream events could not be parsed'),
    incomplete: t('Incomplete'),
    'content-filter': t('Upstream stopped generation due to content filtering'),
    'invalid-json': t(
      'Invalid or incomplete JSON response. Inspect the original payload; it is not a final answer.'
    ),
  }
  const warnings: (string | undefined)[] = [props.notice]
  if (props.capture.truncated) {
    warnings.push(t('Partial capture: this result may be incomplete'))
  }
  if (props.capture.incomplete) {
    warnings.push(t('Capture incomplete; delivery may have been interrupted.'))
  }
  if (props.capture.encoding === 'base64') {
    warnings.push(t('Binary body is shown as Base64'))
  }
  if (!props.capture.request && !props.capture.derived) {
    warnings.push(
      ...(parsed.warnings ?? []).map((warning) => warningLabels[warning])
    )
    if (parsed.reasoningOnly) {
      warnings.push(
        t('Only reasoning was returned; no answer or tool call was received.')
      )
    }
  }
  const visibleWarnings = [...new Set(warnings.filter(Boolean))]
  const notice = visibleWarnings.length ? (
    <div
      role='status'
      className='bg-muted/40 mb-4 space-y-1 rounded-md p-3 text-xs leading-5'
    >
      {visibleWarnings.map((warning) => (
        <div key={warning}>{warning}</div>
      ))}
    </div>
  ) : undefined

  return (
    <Tabs
      value={view}
      onValueChange={setView}
      className='min-h-0 min-w-0 flex-1 gap-0 overflow-hidden'
    >
      <div
        data-audit-chrome='view'
        className='flex shrink-0 flex-wrap items-center justify-between gap-2 border-b px-3 py-2'
      >
        <TabsList variant='line' aria-label={t('Display mode')}>
          <TabsTrigger value='readable'>{t('Readable content')}</TabsTrigger>
          <TabsTrigger value='formatted'>{t('Formatted view')}</TabsTrigger>
          <TabsTrigger value='raw'>{t('Raw data')}</TabsTrigger>
        </TabsList>
        <div className='flex shrink-0 items-center sm:gap-2'>
          {view === 'readable' && documents.length === 1 && (
            <CopyButton
              size='sm'
              className='w-8 px-0 sm:w-auto sm:px-2.5'
              value={displayValue(documents[0].value)}
              tooltip={t('Copy document')}
            />
          )}
          {props.capture.size !== undefined && (
            <span className='text-muted-foreground hidden text-xs sm:inline'>
              {formatNumber(props.capture.size / 1024, locale)} KB
            </span>
          )}
          <CopyButton
            value={props.capture.body}
            size='sm'
            className='w-8 px-0 sm:w-auto sm:px-2.5'
            tooltip={
              props.capture.derived ? t('Copy data') : t('Copy raw body')
            }
          >
            <span className='hidden sm:inline'>
              {props.capture.derived ? t('Copy data') : t('Copy raw body')}
            </span>
          </CopyButton>
        </div>
      </div>
      <TabsContent
        value={view}
        className='flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden'
      >
        {view === 'readable' && props.emptyMessage && (
          <ReadingViewport>
            <EmptyState title={props.emptyMessage} className='min-h-0 py-8' />
          </ReadingViewport>
        )}
        {view === 'readable' && !props.emptyMessage && (
          <DocumentBrowser documents={documents} notice={notice} />
        )}
        {view !== 'readable' && (
          <>
            <div className='text-muted-foreground flex shrink-0 items-center justify-between gap-2 border-b px-3 py-1 text-xs'>
              <span className='min-w-0 truncate'>
                {view === 'raw'
                  ? props.capture.encoding
                  : t('Formatted for inspection; raw data is unchanged.')}
              </span>
              <CopyButton
                size='sm'
                value={view === 'raw' ? props.capture.body : formatted}
                tooltip={t('Copy current content')}
              />
            </div>
            <ReadingViewport key={view}>
              {notice}
              <TextBlock
                text={view === 'raw' ? props.capture.body : formatted}
                code
              />
            </ReadingViewport>
          </>
        )}
      </TabsContent>
    </Tabs>
  )
}

function requestDocumentsForCapture(capture: AuditCapture) {
  if (capture.encoding !== 'utf-8') {
    return [{ id: 'raw', category: 'data' as const, value: capture.body }]
  }
  const value: unknown = JSON.parse(capture.body)
  if (capture.derived) return [{ id: 'data', category: 'data' as const, value }]
  return requestDocuments(value)
}
