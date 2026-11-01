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
import { useTranslation } from 'react-i18next'

import { ErrorState } from '@/components/error-state'
import { Skeleton } from '@/components/ui/skeleton'

import { getBodyAudit } from '../../api'
import { AuditWorkspace } from '../audit-workspace/workspace'

export function BodyAuditSection(props: {
  requestId: string
  enabled: boolean
  requestPath?: string
}) {
  const { t } = useTranslation()
  const query = useQuery({
    queryKey: ['body-audit', props.requestId],
    queryFn: () => getBodyAudit(props.requestId, false),
    enabled: props.enabled && !!props.requestId,
    retry: false,
  })
  if (!props.enabled || !props.requestId) return null
  return (
    <section
      aria-label={t('Body Audit')}
      className='flex h-full min-h-0 min-w-0 flex-col overflow-hidden'
    >
      {query.isLoading && <Skeleton className='min-h-24 flex-1' />}
      {query.isError && (
        <ErrorState
          className='min-h-0 flex-1'
          onRetry={() => void query.refetch()}
        />
      )}
      {!query.isError && query.data && (
        <AuditWorkspace
          key={props.requestId}
          audit={query.data}
          requestPath={props.requestPath}
        />
      )}
      {!query.isLoading && !query.isError && !query.data && (
        <p className='text-muted-foreground p-6 text-sm'>
          {t('No body audit was stored for this request')}
        </p>
      )}
    </section>
  )
}
