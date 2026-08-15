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
import { useQuery } from '@tanstack/react-query'
import {
  AlertTriangle,
  ArrowDownToLine,
  ArrowUpFromLine,
  Check,
  Copy,
  FileJson2,
  FileText,
  Radio,
} from 'lucide-react'
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import { StatusBadge } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import { IconBadge } from '@/components/ui/icon-badge'
import { Skeleton } from '@/components/ui/skeleton'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { useCopyToClipboard } from '@/hooks/use-copy-to-clipboard'
import { cn } from '@/lib/utils'

import { getBodyAudit } from '../../api'
import type { BodyAudit } from '../../types'

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`
}

function formatBody(body: string, encoding: 'utf-8' | 'base64'): string {
  if (!body || encoding === 'base64') return body
  const trimmed = body.trim()
  if (!trimmed.startsWith('{') && !trimmed.startsWith('[')) return body
  try {
    return JSON.stringify(JSON.parse(body), null, 2)
  } catch {
    return body
  }
}

function stringContent(value: unknown): string {
  if (typeof value === 'string') return value
  if (!Array.isArray(value)) return ''
  return value
    .map((item) => {
      if (typeof item === 'string') return item
      if (!item || typeof item !== 'object') return ''
      const part = item as Record<string, unknown>
      if (typeof part.text === 'string') return part.text
      if (typeof part.content === 'string') return part.content
      return ''
    })
    .join('')
}

function extractResponseText(value: unknown): string {
  if (!value || typeof value !== 'object') return ''
  const data = value as Record<string, unknown>

  if (typeof data.delta === 'string') return data.delta
  if (typeof data.text === 'string') return data.text
  if (typeof data.completion === 'string') return data.completion
  if (typeof data.output_text === 'string') return data.output_text

  if (data.delta && typeof data.delta === 'object') {
    const delta = data.delta as Record<string, unknown>
    const deltaText =
      stringContent(delta.content) ||
      stringContent(delta.text) ||
      stringContent(delta.output_text)
    if (deltaText) return deltaText
  }

  if (Array.isArray(data.choices)) {
    return data.choices
      .map((choice) => {
        if (!choice || typeof choice !== 'object') return ''
        const item = choice as Record<string, unknown>
        if (item.delta && typeof item.delta === 'object') {
          const delta = item.delta as Record<string, unknown>
          return stringContent(delta.content) || stringContent(delta.text)
        }
        if (item.message && typeof item.message === 'object') {
          return stringContent(
            (item.message as Record<string, unknown>).content
          )
        }
        return stringContent(item.text)
      })
      .join('')
  }

  if (Array.isArray(data.candidates)) {
    return data.candidates
      .map((candidate) => {
        if (!candidate || typeof candidate !== 'object') return ''
        const content = (candidate as Record<string, unknown>).content
        if (!content || typeof content !== 'object') return ''
        return stringContent((content as Record<string, unknown>).parts)
      })
      .join('')
  }

  if (Array.isArray(data.content)) return stringContent(data.content)
  if (data.message && typeof data.message === 'object') {
    return stringContent((data.message as Record<string, unknown>).content)
  }
  if (data.response && typeof data.response === 'object') {
    return extractResponseText(data.response)
  }
  return ''
}

function getReadableResponse(body: string): {
  text: string
  isStream: boolean
} {
  const trimmed = body.trim()
  if (!trimmed) return { text: '', isStream: false }

  const dataLines = body
    .split(/\r?\n/)
    .filter((line) => line.startsWith('data:'))
    .map((line) => line.slice(5).trim())
    .filter((line) => line && line !== '[DONE]')

  if (dataLines.length > 0) {
    let text = ''
    for (const line of dataLines) {
      try {
        text += extractResponseText(JSON.parse(line))
      } catch {
        // Keep malformed/non-JSON events available in the raw response tab.
      }
    }
    return { text, isStream: true }
  }

  try {
    return { text: extractResponseText(JSON.parse(trimmed)), isStream: false }
  } catch {
    return { text: body, isStream: false }
  }
}

function PayloadPanel(props: {
  title: string
  body: string
  encoding: 'utf-8' | 'base64'
  size: number
  truncated: boolean
  contentType?: string
  icon: React.ReactNode
}) {
  const { t } = useTranslation()
  const { copiedText, copyToClipboard } = useCopyToClipboard({ notify: false })
  const formattedBody = useMemo(
    () => formatBody(props.body, props.encoding),
    [props.body, props.encoding]
  )
  const copied = copiedText === formattedBody

  return (
    <div className='border-border bg-background overflow-hidden rounded-lg border shadow-sm'>
      <div className='bg-muted/35 flex min-h-10 items-center justify-between gap-3 border-b px-3 py-2'>
        <div className='flex min-w-0 items-center gap-2'>
          <span className='text-muted-foreground'>{props.icon}</span>
          <span className='truncate text-xs font-medium'>{props.title}</span>
          <StatusBadge
            label={formatBytes(props.size)}
            variant='neutral'
            size='sm'
            copyable={false}
          />
          {props.contentType && (
            <span className='text-muted-foreground hidden truncate font-mono text-[10px] sm:inline'>
              {props.contentType}
            </span>
          )}
        </div>
        <Button
          variant='ghost'
          size='sm'
          className='h-7 shrink-0 gap-1.5 px-2'
          onClick={() => copyToClipboard(formattedBody)}
          aria-label={t('Copy to clipboard')}
        >
          {copied ? (
            <Check className='size-3.5 text-emerald-600 dark:text-emerald-400' />
          ) : (
            <Copy className='size-3.5' />
          )}
          <span className='text-xs'>{copied ? t('Copied') : t('Copy')}</span>
        </Button>
      </div>
      {(props.truncated || props.encoding === 'base64') && (
        <div className='flex items-center gap-2 border-b border-amber-400/30 bg-amber-50 px-3 py-2 text-[11px] text-amber-700 dark:bg-amber-950/20 dark:text-amber-300'>
          <AlertTriangle className='size-3.5 shrink-0' aria-hidden='true' />
          {props.truncated
            ? t('The body exceeded the audit size limit and was truncated')
            : t('Binary body is displayed as Base64')}
        </div>
      )}
      <pre
        className={cn(
          'scrollbar-thin min-h-44 max-h-[min(44dvh,440px)] overflow-auto p-4',
          'font-mono text-[12px] leading-[1.65] text-foreground selection:bg-blue-500/25',
          'whitespace-pre-wrap break-words'
        )}
      >
        {formattedBody || t('Empty body')}
      </pre>
    </div>
  )
}

function ResponsePanel(props: { audit: BodyAudit }) {
  const { t } = useTranslation()
  const readable = useMemo(
    () => getReadableResponse(props.audit.response_body),
    [props.audit.response_body]
  )
  const { copiedText, copyToClipboard } = useCopyToClipboard({ notify: false })
  const copied = copiedText === readable.text

  return (
    <Tabs defaultValue='result' className='gap-2'>
      <div className='flex items-center justify-between gap-2'>
        <TabsList className='h-8'>
          <TabsTrigger value='result' className='h-7 gap-1.5 px-2.5 text-xs'>
            <FileText className='size-3.5' aria-hidden='true' />
            {t('Final Result')}
          </TabsTrigger>
          <TabsTrigger value='raw' className='h-7 gap-1.5 px-2.5 text-xs'>
            <Radio className='size-3.5' aria-hidden='true' />
            {t('Raw Response')}
          </TabsTrigger>
        </TabsList>
        {readable.isStream && (
          <StatusBadge
            label={t('Stream merged')}
            variant='blue'
            size='sm'
            copyable={false}
          />
        )}
      </div>
      <TabsContent value='result'>
        <div className='border-border bg-background overflow-hidden rounded-lg border shadow-sm'>
          <div className='bg-muted/35 flex min-h-10 items-center justify-between gap-3 border-b px-3 py-2'>
            <div className='flex items-center gap-2 text-xs font-medium'>
              <FileText
                className='text-muted-foreground size-3.5'
                aria-hidden='true'
              />
              {t('Readable final response')}
            </div>
            <Button
              variant='ghost'
              size='sm'
              className='h-7 gap-1.5 px-2'
              onClick={() => copyToClipboard(readable.text)}
              aria-label={t('Copy to clipboard')}
            >
              {copied ? (
                <Check className='size-3.5 text-emerald-600 dark:text-emerald-400' />
              ) : (
                <Copy className='size-3.5' />
              )}
              <span className='text-xs'>
                {copied ? t('Copied') : t('Copy')}
              </span>
            </Button>
          </div>
          <div className='max-h-[min(44dvh,440px)] min-h-44 scrollbar-thin overflow-auto p-4 text-sm leading-7 break-words whitespace-pre-wrap'>
            {readable.text || t('No readable text was extracted')}
          </div>
        </div>
      </TabsContent>
      <TabsContent value='raw'>
        <PayloadPanel
          title={t('Raw response returned by upstream')}
          body={props.audit.response_body}
          encoding={props.audit.response_body_encoding}
          size={props.audit.response_body_size}
          truncated={props.audit.response_body_truncated}
          contentType={props.audit.response_content_type}
          icon={<Radio className='size-3.5' aria-hidden='true' />}
        />
      </TabsContent>
    </Tabs>
  )
}

function AuditContent(props: { audit: BodyAudit }) {
  const { t } = useTranslation()
  const responseStatusVariant =
    props.audit.response_status >= 200 && props.audit.response_status < 300
      ? 'green'
      : 'red'

  return (
    <Tabs defaultValue='request' className='gap-2.5'>
      <div className='flex flex-wrap items-center justify-between gap-2'>
        <TabsList className='h-9'>
          <TabsTrigger value='request' className='gap-1.5 px-3'>
            <ArrowUpFromLine className='size-3.5' aria-hidden='true' />
            {t('Sent Request')}
            <span className='text-muted-foreground font-mono text-[10px]'>
              {formatBytes(props.audit.request_body_size)}
            </span>
          </TabsTrigger>
          <TabsTrigger value='response' className='gap-1.5 px-3'>
            <ArrowDownToLine className='size-3.5' aria-hidden='true' />
            {t('Upstream Response')}
            <span className='text-muted-foreground font-mono text-[10px]'>
              {formatBytes(props.audit.response_body_size)}
            </span>
          </TabsTrigger>
        </TabsList>
        <div className='flex items-center gap-1.5'>
          {props.audit.response_status > 0 && (
            <StatusBadge
              label={`HTTP ${props.audit.response_status}`}
              variant={responseStatusVariant}
              size='sm'
              copyable={false}
            />
          )}
          <StatusBadge
            label={
              props.audit.response_complete
                ? t('Capture complete')
                : t('Capture incomplete')
            }
            variant={props.audit.response_complete ? 'green' : 'orange'}
            size='sm'
            copyable={false}
          />
        </div>
      </div>
      <TabsContent value='request'>
        <PayloadPanel
          title={t('Final request sent to upstream')}
          body={props.audit.request_body}
          encoding={props.audit.request_body_encoding}
          size={props.audit.request_body_size}
          truncated={props.audit.request_body_truncated}
          icon={<ArrowUpFromLine className='size-3.5' aria-hidden='true' />}
        />
      </TabsContent>
      <TabsContent value='response'>
        <ResponsePanel audit={props.audit} />
      </TabsContent>
    </Tabs>
  )
}

export function BodyAuditSection(props: {
  requestId: string
  enabled: boolean
}) {
  const { t } = useTranslation()
  const query = useQuery({
    queryKey: ['body-audit', props.requestId],
    queryFn: () => getBodyAudit(props.requestId),
    enabled: props.enabled && props.requestId.length > 0,
    retry: false,
  })

  if (!props.enabled || !props.requestId) return null

  return (
    <section className='min-w-0 space-y-2' aria-label={t('Body Audit')}>
      <div className='flex items-center gap-1.5 text-xs font-semibold'>
        <IconBadge tone='info' size='xs'>
          <FileJson2 className='size-3.5' aria-hidden='true' />
        </IconBadge>
        <span>{t('Body Audit')}</span>
        <StatusBadge
          label={t('Admin Only')}
          variant='blue'
          size='sm'
          copyable={false}
        />
      </div>
      {query.isLoading && (
        <div className='space-y-2 rounded-lg border p-3'>
          <Skeleton className='h-8 w-72 max-w-full' />
          <Skeleton className='h-48 w-full' />
        </div>
      )}
      {!query.isLoading && query.data && <AuditContent audit={query.data} />}
      {!query.isLoading && !query.data && (
        <div className='bg-muted/30 text-muted-foreground rounded-lg border border-dashed px-4 py-6 text-center text-xs'>
          {t('No body audit was stored for this request')}
        </div>
      )}
    </section>
  )
}
