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
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { StatusBadge } from '@/components/status-badge'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'

import type { AuditCapture } from '../../lib/audit-workspace'
import type { BodyAudit } from '../../types'
import { AuditExecution } from './execution'
import { AuditPayload } from './payload'
import { ReadingViewport } from './reader'

export function AuditWorkspace(props: {
  audit: BodyAudit
  requestPath?: string
}) {
  const { t } = useTranslation()
  const [source, setSource] = useState('result')
  const audit = props.audit
  const hasClient =
    audit.client_response_status > 0 ||
    audit.client_response_body_size > 0 ||
    audit.client_response_complete
  const sources = [
    { id: 'result', label: t('Final Result') },
    { id: 'request', label: t('Sent Request') },
    { id: 'client', label: t('Client Response') },
    { id: 'upstream', label: t('Upstream Response') },
    { id: 'execution', label: t('Execution details') },
  ]
  const useClient = source === 'client' || (source === 'result' && hasClient)
  const responseStatus = useClient
    ? audit.client_response_status
    : audit.response_status
  let capture: AuditCapture = {
    body: audit.response_body,
    size: audit.response_body_size,
    encoding: audit.response_body_encoding,
    contentType: audit.response_content_type,
    truncated: audit.response_body_truncated,
    incomplete: !audit.response_complete,
  }
  if (useClient) {
    capture = {
      body: audit.client_response_body,
      size: audit.client_response_body_size,
      encoding: audit.client_response_body_encoding,
      contentType: audit.client_response_content_type,
      truncated: audit.client_response_body_truncated,
      incomplete: !audit.client_response_complete,
    }
  }
  if (source === 'request') {
    capture = {
      body: audit.request_body,
      size: audit.request_body_size,
      encoding: audit.request_body_encoding,
      truncated: audit.request_body_truncated,
      request: true,
    }
  }
  return (
    <Tabs
      value={source}
      onValueChange={setSource}
      className='h-full min-h-0 min-w-0 gap-0 overflow-hidden rounded-lg border'
    >
      <div
        data-audit-chrome='source'
        className='bg-muted/30 flex shrink-0 items-center gap-2 border-b px-3 py-2'
      >
        <TabsList
          variant='line'
          aria-label={t('Audit source')}
          className='hidden max-w-full min-w-0 lg:flex lg:justify-start'
        >
          {sources.map((item) => (
            <TabsTrigger
              key={item.id}
              value={item.id}
              className='flex-none px-3'
            >
              {item.label}
            </TabsTrigger>
          ))}
        </TabsList>
        <NativeSelect
          className='min-w-0 flex-1 lg:hidden'
          aria-label={t('Audit source')}
          value={source}
          onChange={(event) => setSource(event.target.value)}
        >
          {sources.map((item) => (
            <NativeSelectOption key={item.id} value={item.id}>
              {item.label}
            </NativeSelectOption>
          ))}
        </NativeSelect>
        <div className='ml-auto shrink-0'>
          {responseStatus > 0 &&
            source !== 'execution' &&
            source !== 'request' && (
              <StatusBadge
                label={`HTTP ${responseStatus}`}
                size='sm'
                copyable={false}
                variant={responseStatus < 400 ? 'green' : 'red'}
              />
            )}
        </div>
      </div>
      <TabsContent
        value={source}
        className='flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden'
      >
        {source === 'execution' && <AuditExecution audit={audit} />}
        {source === 'client' && !hasClient && (
          <ReadingViewport>
            <p className='text-muted-foreground text-sm'>
              {t('Client response was not captured for this legacy record')}
            </p>
          </ReadingViewport>
        )}
        {source !== 'execution' && (source !== 'client' || hasClient) && (
          <AuditPayload
            key={source}
            capture={capture}
            requestPath={props.requestPath}
            final={source === 'result'}
            notice={
              source === 'result' && !hasClient
                ? t(
                    'Client response was not captured. Showing upstream content; delivery to the client is unverified.'
                  )
                : undefined
            }
          />
        )}
      </TabsContent>
    </Tabs>
  )
}
