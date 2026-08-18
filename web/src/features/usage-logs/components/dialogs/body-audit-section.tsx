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
  GitBranch,
  Radio,
} from 'lucide-react'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { StatusBadge } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import { IconBadge } from '@/components/ui/icon-badge'
import { Skeleton } from '@/components/ui/skeleton'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { useCopyToClipboard } from '@/hooks/use-copy-to-clipboard'
import { cn } from '@/lib/utils'

import { getBodyAudit } from '../../api'
import { parseBodyAuditResponse } from '../../lib/body-audit-response'
import { diffJsonText } from '../../lib/json-diff'
import type { AuditAttempt, BodyAudit } from '../../types'
import { BodyAuditResultContent } from '../body-audit-result'

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

function ResponsePanel(props: { audit: BodyAudit; requestPath?: string }) {
  const { t } = useTranslation()
  const hasClientResponse =
    props.audit.client_response_status > 0 ||
    props.audit.client_response_body_size > 0 ||
    props.audit.client_response_complete
  const result = useMemo(
    () =>
      parseBodyAuditResponse({
        body: hasClientResponse
          ? props.audit.client_response_body
          : props.audit.response_body,
        encoding: hasClientResponse
          ? props.audit.client_response_body_encoding
          : props.audit.response_body_encoding,
        contentType: hasClientResponse
          ? props.audit.client_response_content_type
          : props.audit.response_content_type,
        requestPath: props.requestPath,
      }),
    [hasClientResponse, props.audit, props.requestPath]
  )
  const copyText = useMemo(() => {
    if (result.kind === 'text') return result.text
    if (result.media.length > 0) {
      return result.media.map((item) => item.source).join('\n')
    }
    if (result.structured == null) return ''
    if (typeof result.structured === 'string') return result.structured
    return JSON.stringify(result.structured, null, 2)
  }, [result])
  const { copiedText, copyToClipboard } = useCopyToClipboard({ notify: false })
  const copied = copiedText === copyText

  return (
    <Tabs defaultValue='result' className='gap-2'>
      <div className='flex flex-wrap items-center justify-between gap-2'>
        <TabsList className='h-auto max-w-full flex-wrap'>
          <TabsTrigger value='result' className='h-7 gap-1.5 px-2.5 text-xs'>
            <FileText className='size-3.5' aria-hidden='true' />
            {t('Final Result')}
          </TabsTrigger>
          <TabsTrigger value='client' className='h-7 gap-1.5 px-2.5 text-xs'>
            <FileJson2 className='size-3.5' aria-hidden='true' />
            {t('Client Response')}
          </TabsTrigger>
          <TabsTrigger value='upstream' className='h-7 gap-1.5 px-2.5 text-xs'>
            <Radio className='size-3.5' aria-hidden='true' />
            {t('Upstream Response')}
          </TabsTrigger>
        </TabsList>
        {result.isStream && (
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
              disabled={!copyText}
              onClick={() => copyToClipboard(copyText)}
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
          <div className='max-h-[min(44dvh,440px)] min-h-44 scrollbar-thin overflow-auto p-4'>
            <BodyAuditResultContent
              result={result}
              emptyLabel={t('No readable text was extracted')}
              imageAlt={t('Generated image')}
            />
          </div>
        </div>
      </TabsContent>
      <TabsContent value='client'>
        {hasClientResponse ? (
          <PayloadPanel
            title={t('Exact response returned by New API')}
            body={props.audit.client_response_body}
            encoding={props.audit.client_response_body_encoding}
            size={props.audit.client_response_body_size}
            truncated={props.audit.client_response_body_truncated}
            contentType={props.audit.client_response_content_type}
            icon={<FileJson2 className='size-3.5' aria-hidden='true' />}
          />
        ) : (
          <div className='bg-muted/30 text-muted-foreground rounded-lg border border-dashed px-4 py-6 text-center text-xs'>
            {t('Client response was not captured for this legacy record')}
          </div>
        )}
      </TabsContent>
      <TabsContent value='upstream'>
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

function AttemptTimeline(props: { attempts: AuditAttempt[] }) {
  const { t } = useTranslation()
  const [selectedId, setSelectedId] = useState(props.attempts.at(-1)?.id ?? 0)
  const selected =
    props.attempts.find((attempt) => attempt.id === selectedId) ??
    props.attempts.at(-1)
  const previous = selected
    ? props.attempts.find(
        (attempt) => attempt.attempt_no === selected.attempt_no - 1
      )
    : undefined
  const requestDiff = useMemo(() => {
    if (
      !selected ||
      !previous ||
      selected.request_body_encoding !== 'utf-8' ||
      previous.request_body_encoding !== 'utf-8'
    ) {
      return null
    }
    return diffJsonText(previous.request_body, selected.request_body)
  }, [previous, selected])

  if (!selected) return null

  return (
    <div className='space-y-2.5 rounded-lg border p-3'>
      <div className='flex items-center gap-2 text-xs font-semibold'>
        <GitBranch
          className='text-muted-foreground size-3.5'
          aria-hidden='true'
        />
        <span>{t('Upstream attempt timeline')}</span>
        <StatusBadge
          label={t('{{count}} attempts', { count: props.attempts.length })}
          variant={props.attempts.length > 1 ? 'orange' : 'neutral'}
          size='sm'
          copyable={false}
        />
      </div>
      <div className='scrollbar-thin flex gap-2 overflow-x-auto pb-1'>
        {props.attempts.map((attempt) => {
          const succeeded = attempt.state === 'succeeded'
          const selectedAttempt = attempt.id === selected.id
          return (
            <button
              type='button'
              key={attempt.id}
              onClick={() => setSelectedId(attempt.id)}
              className={cn(
                'min-w-40 rounded-md border px-3 py-2 text-left transition-colors',
                selectedAttempt
                  ? 'border-primary bg-primary/5'
                  : 'bg-muted/20 hover:bg-muted/45'
              )}
              aria-pressed={selectedAttempt}
            >
              <div className='flex items-center justify-between gap-2'>
                <span className='text-xs font-medium'>
                  {t('Attempt {{number}}', { number: attempt.attempt_no + 1 })}
                </span>
                <span
                  className={cn(
                    'size-2 rounded-full',
                    succeeded ? 'bg-emerald-500' : 'bg-red-500'
                  )}
                  aria-hidden='true'
                />
              </div>
              <div className='text-muted-foreground mt-1 font-mono text-[10px]'>
                {t('Channel')} #{attempt.channel_id}
                {attempt.http_status > 0
                  ? ` · HTTP ${attempt.http_status}`
                  : ''}
              </div>
            </button>
          )
        })}
      </div>
      <div className='bg-muted/20 flex flex-wrap gap-x-4 gap-y-1 rounded-md border px-3 py-2 font-mono text-[10px]'>
        <span>
          {selected.request_model || '—'} → {selected.upstream_model || '—'}
        </span>
        <span>
          {selected.request_format || '—'} → {selected.upstream_format || '—'}
        </span>
        <span className='max-w-full truncate'>{selected.target || '—'}</span>
        <span>{selected.terminal_kind || selected.state}</span>
      </div>
      <Tabs defaultValue='attempt-request' className='gap-2'>
        <TabsList className='h-8'>
          <TabsTrigger value='attempt-request' className='h-7 text-xs'>
            {t('Attempt request')}
          </TabsTrigger>
          <TabsTrigger value='attempt-response' className='h-7 text-xs'>
            {t('Attempt response')}
          </TabsTrigger>
          {requestDiff && (
            <TabsTrigger value='attempt-diff' className='h-7 text-xs'>
              {t('Changes from previous attempt')}
            </TabsTrigger>
          )}
        </TabsList>
        <TabsContent value='attempt-request'>
          <PayloadPanel
            title={t('Exact payload sent in this attempt')}
            body={selected.request_body}
            encoding={selected.request_body_encoding}
            size={selected.request_body_size}
            truncated={selected.request_body_truncated}
            icon={<ArrowUpFromLine className='size-3.5' aria-hidden='true' />}
          />
        </TabsContent>
        <TabsContent value='attempt-response'>
          <PayloadPanel
            title={t('Exact upstream response for this attempt')}
            body={selected.response_body}
            encoding={selected.response_body_encoding}
            size={selected.response_body_size}
            truncated={selected.response_body_truncated}
            icon={<ArrowDownToLine className='size-3.5' aria-hidden='true' />}
          />
        </TabsContent>
        {requestDiff && (
          <TabsContent value='attempt-diff'>
            <PayloadPanel
              title={t('JSON changes from the previous attempt')}
              body={JSON.stringify(requestDiff)}
              encoding='utf-8'
              size={
                new TextEncoder().encode(JSON.stringify(requestDiff)).length
              }
              truncated={requestDiff.length >= 500}
              contentType='application/json'
              icon={<GitBranch className='size-3.5' aria-hidden='true' />}
            />
          </TabsContent>
        )}
      </Tabs>
    </div>
  )
}

function AuditContent(props: { audit: BodyAudit; requestPath?: string }) {
  const { t } = useTranslation()
  const responseStatusVariant =
    props.audit.response_status >= 200 && props.audit.response_status < 300
      ? 'green'
      : 'red'

  return (
    <div className='space-y-3'>
      {props.audit.attempts && props.audit.attempts.length > 0 && (
        <AttemptTimeline attempts={props.audit.attempts} />
      )}
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
              {t('Response')}
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
          <ResponsePanel audit={props.audit} requestPath={props.requestPath} />
        </TabsContent>
      </Tabs>
    </div>
  )
}

export function BodyAuditSection(props: {
  requestId: string
  enabled: boolean
  requestPath?: string
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
      {!query.isLoading && query.data && (
        <AuditContent audit={query.data} requestPath={props.requestPath} />
      )}
      {!query.isLoading && !query.data && (
        <div className='bg-muted/30 text-muted-foreground rounded-lg border border-dashed px-4 py-6 text-center text-xs'>
          {t('No body audit was stored for this request')}
        </div>
      )}
    </section>
  )
}
