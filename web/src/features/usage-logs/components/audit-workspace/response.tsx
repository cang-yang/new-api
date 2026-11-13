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

import { Button } from '@/components/ui/button'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'

import type { AuditCapture } from '../../lib/audit-workspace'
import { AuditPayload } from './payload'
import { ReadingViewport } from './reader'

export function AuditResponse(props: {
  client?: AuditCapture
  upstream: AuditCapture
  clientStatus: number
  upstreamStatus: number
  requestPath?: string
}) {
  const { t } = useTranslation()
  const [view, setView] = useState('readable')
  const [mobileSource, setMobileSource] = useState('client')
  const [comparisonView, setComparisonView] = useState<'readable' | 'raw'>(
    'readable'
  )
  const complete =
    !!props.client &&
    !props.client.incomplete &&
    !props.client.truncated &&
    !props.upstream.incomplete &&
    !props.upstream.truncated
  // Compare captured data, not normalized reader output. Equal partial captures
  // and missing client data must never be presented as equal complete responses.
  const identical =
    complete &&
    props.client?.body === props.upstream.body &&
    props.client?.encoding === props.upstream.encoding &&
    props.client?.contentType === props.upstream.contentType &&
    props.clientStatus === props.upstreamStatus
  let comparison = t('The captured responses differ.')
  if (!complete) {
    comparison = t(
      'Capture is missing or incomplete; a full comparison is unavailable.'
    )
  } else if (identical) {
    comparison = t('Upstream and client responses are identical.')
  }
  const fallback = !props.client
    ? t(
        'Client response was not captured. Showing upstream content; delivery to the client is unverified.'
      )
    : undefined
  const sources = [
    {
      id: 'upstream',
      title: t('Before processing · Upstream'),
      capture: props.upstream,
      status: props.upstreamStatus,
    },
    {
      id: 'client',
      title: t('After processing · Client'),
      capture: props.client,
      status: props.clientStatus,
    },
  ]
  return (
    <Tabs
      value={view}
      onValueChange={setView}
      className='min-h-0 min-w-0 flex-1 gap-0 overflow-hidden'
    >
      <div className='shrink-0 overflow-x-auto border-b px-3 py-2'>
        <TabsList aria-label={t('Response inspection')} className='min-w-max'>
          <TabsTrigger value='readable'>{t('Readable content')}</TabsTrigger>
          <TabsTrigger value='raw'>{t('Raw data')}</TabsTrigger>
          <TabsTrigger value='compare'>
            {t('Compare with upstream')}
          </TabsTrigger>
        </TabsList>
      </div>
      <TabsContent
        value={view}
        className='flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden'
      >
        {view === 'readable' && (
          <AuditPayload
            capture={props.client ?? props.upstream}
            requestPath={props.requestPath}
            fixedView='readable'
            notice={fallback}
          />
        )}
        {view === 'raw' &&
          (props.client ? (
            <AuditPayload
              capture={props.client}
              requestPath={props.requestPath}
              fixedView='raw'
            />
          ) : (
            <ReadingViewport>
              {t('Client response was not captured for this legacy record')}
            </ReadingViewport>
          ))}
        {view === 'compare' && (
          <>
            <div role='status' className='shrink-0 border-b px-3 py-2 text-sm'>
              <p>{comparison}</p>
              <p className='text-muted-foreground mt-1 text-xs'>
                {t(
                  'Compares captured bodies, content types and HTTP status. Formatting differences also count.'
                )}
              </p>
              <div className='mt-2 flex gap-2'>
                <Button
                  size='sm'
                  variant={
                    comparisonView === 'readable' ? 'secondary' : 'ghost'
                  }
                  aria-pressed={comparisonView === 'readable'}
                  onClick={() => setComparisonView('readable')}
                >
                  {t('Readable content')}
                </Button>
                <Button
                  size='sm'
                  variant={comparisonView === 'raw' ? 'secondary' : 'ghost'}
                  aria-pressed={comparisonView === 'raw'}
                  onClick={() => setComparisonView('raw')}
                >
                  {t('Raw data')}
                </Button>
              </div>
            </div>
            {identical ? (
              <AuditPayload
                capture={props.upstream}
                requestPath={props.requestPath}
                fixedView={comparisonView}
              />
            ) : (
              <>
                <Select
                  value={mobileSource}
                  onValueChange={(value) => {
                    if (value) setMobileSource(value)
                  }}
                  items={sources.map((source) => ({
                    value: source.id,
                    label: source.title,
                  }))}
                >
                  <SelectTrigger
                    aria-label={t('Comparison source')}
                    className='m-2 shrink-0 lg:hidden'
                  >
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectGroup>
                      {sources.map((source) => (
                        <SelectItem key={source.id} value={source.id}>
                          {source.title}
                        </SelectItem>
                      ))}
                    </SelectGroup>
                  </SelectContent>
                </Select>
                <div className='flex min-h-0 min-w-0 flex-1 overflow-hidden lg:divide-x'>
                  {sources.map((source) => (
                    <section
                      key={source.id}
                      aria-label={source.title}
                      className={`min-h-0 min-w-0 flex-1 flex-col overflow-hidden ${mobileSource === source.id ? 'flex' : 'hidden lg:flex'}`}
                    >
                      <h3 className='bg-muted/30 flex shrink-0 items-center justify-between gap-2 border-b px-3 py-2 text-sm font-medium'>
                        {source.title}
                        {source.status > 0 && (
                          <span className='text-muted-foreground text-xs'>
                            HTTP {source.status}
                          </span>
                        )}
                      </h3>
                      {source.capture ? (
                        <AuditPayload
                          capture={source.capture}
                          requestPath={props.requestPath}
                          fixedView={comparisonView}
                        />
                      ) : (
                        <ReadingViewport>
                          {t(
                            'Client response was not captured for this legacy record'
                          )}
                        </ReadingViewport>
                      )}
                    </section>
                  ))}
                </div>
              </>
            )}
          </>
        )}
      </TabsContent>
    </Tabs>
  )
}
