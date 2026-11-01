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
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ErrorState } from '@/components/error-state'
import { Button } from '@/components/ui/button'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Skeleton } from '@/components/ui/skeleton'

import { getBodyAudit } from '../../api'
import type { AuditCapture } from '../../lib/audit-workspace'
import { diffJsonText } from '../../lib/json-diff'
import type { BodyAudit } from '../../types'
import { AuditReplayDialog } from '../dialogs/audit-replay-dialog'
import { AuditPayload } from './payload'
import { ReadingViewport } from './reader'

export function AuditExecution(props: { audit: BodyAudit }) {
  const { t } = useTranslation()
  const [selectedId, setSelectedId] = useState(props.audit.attempts?.at(-1)?.id)
  const [part, setPart] = useState('overview')
  const [replayOpen, setReplayOpen] = useState(false)
  const multiple = (props.audit.attempts?.length ?? 0) > 1
  const query = useQuery({
    queryKey: ['body-audit', props.audit.request_id, 'attempt-bodies'],
    queryFn: () => getBodyAudit(props.audit.request_id, true),
    enabled: multiple,
    retry: false,
  })
  const attempts = query.data?.attempts ?? props.audit.attempts ?? []
  const selected =
    attempts.find((item) => item.id === selectedId) ?? attempts.at(-1)
  const previous =
    selected &&
    attempts.find((item) => item.attempt_no === selected.attempt_no - 1)
  const diff = useMemo(() => {
    if (
      !selected ||
      !previous ||
      selected.request_body_encoding !== 'utf-8' ||
      previous.request_body_encoding !== 'utf-8'
    ) {
      return null
    }
    return diffJsonText(previous.request_body, selected.request_body)
  }, [selected, previous])
  if (!selected) {
    return (
      <ReadingViewport>
        <p>{t('No execution attempts were recorded')}</p>
      </ReadingViewport>
    )
  }
  let notice: string | undefined
  let capture: AuditCapture = {
    body: JSON.stringify({
      [t('Channel')]: `${selected.channel_name} #${selected.channel_id}`,
      HTTP: selected.http_status,
      [t('Status')]: selected.state,
      [t('Outcome')]: selected.outcome,
      [t('Response Time')]: `${selected.duration_ms} ms`,
      [t('Request Model')]: selected.request_model,
      [t('Actual Model')]: selected.upstream_model,
      [t('Request Conversion')]:
        `${selected.request_format} → ${selected.upstream_format}`,
      [t('Target')]: selected.target,
      [t('End Reason')]: selected.terminal_kind,
      [t('Error')]: selected.error_message || selected.error_code,
    }),
    encoding: 'utf-8',
    derived: true,
  }
  if (part === 'request') {
    const data = multiple ? selected : props.audit
    capture = {
      body: data.request_body,
      encoding: data.request_body_encoding,
      truncated: data.request_body_truncated,
      request: true,
    }
  } else if (part === 'response') {
    const data = multiple ? selected : props.audit
    capture = {
      body: data.response_body,
      encoding: data.response_body_encoding,
      truncated: data.response_body_truncated,
      incomplete: !selected.complete,
    }
  } else if (part === 'config') {
    capture.body = JSON.stringify(
      selected.config_snapshot?.canonical_json ?? null
    )
    notice = selected.config_snapshot
      ? `${t('Immutable execution config')} · schema v${selected.config_snapshot.schema_version} · ${selected.config_snapshot.digest}`
      : t('No configuration snapshot was recorded')
  } else if (part === 'diff') {
    capture.body = JSON.stringify(diff)
    if (!previous) {
      notice = t('The first attempt has no previous request to compare.')
    } else if (!diff) {
      notice = t('These request bodies cannot be compared as JSON.')
    } else if (!diff.length) notice = t('No differences')
    else if (diff.length >= 500) {
      notice = t(
        'Difference limit reached; compare the raw requests for the full data.'
      )
    } else notice = t('JSON changes from the previous attempt')
  }
  return (
    <div className='flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden'>
      <div
        data-audit-chrome='execution'
        className='flex shrink-0 flex-wrap items-center gap-2 border-b px-3 py-2'
      >
        <NativeSelect
          className='min-w-0 flex-1'
          aria-label={t('Upstream attempt timeline')}
          value={String(selected.id)}
          onChange={(event) => setSelectedId(Number(event.target.value))}
        >
          {attempts.map((item) => (
            <NativeSelectOption key={item.id} value={String(item.id)}>
              {t('Attempt {{number}}', { number: item.attempt_no + 1 })} · #
              {item.channel_id} · HTTP {item.http_status}
            </NativeSelectOption>
          ))}
        </NativeSelect>
        <Button size='sm' variant='outline' onClick={() => setReplayOpen(true)}>
          {t('Replay selected attempt')}
        </Button>
        <NativeSelect
          className='w-full sm:w-auto'
          aria-label={t('Attempt content')}
          value={part}
          onChange={(event) => setPart(event.target.value)}
        >
          <NativeSelectOption value='overview'>
            {t('Overview')}
          </NativeSelectOption>
          <NativeSelectOption value='request'>
            {t('Attempt request')}
          </NativeSelectOption>
          <NativeSelectOption value='response'>
            {t('Attempt response')}
          </NativeSelectOption>
          <NativeSelectOption value='diff'>
            {t('Changes from previous attempt')}
          </NativeSelectOption>
          <NativeSelectOption value='config'>
            {t('Execution config')}
          </NativeSelectOption>
        </NativeSelect>
      </div>
      {multiple && query.isLoading && (
        <Skeleton className='m-4 min-h-24 flex-1' />
      )}
      {multiple && query.isError && (
        <ErrorState
          className='min-h-0 flex-1'
          onRetry={() => void query.refetch()}
        />
      )}
      {(!multiple || (!query.isLoading && !query.isError)) && (
        <AuditPayload
          key={`${selected.id}:${part}`}
          capture={capture}
          notice={notice}
        />
      )}
      <AuditReplayDialog
        open={replayOpen}
        onOpenChange={setReplayOpen}
        requestId={props.audit.request_id}
        attemptId={selected.id}
      />
    </div>
  )
}
