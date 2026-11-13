import { useQuery } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'

import { getChannelAdaptationHealth } from '../api'

const labels = {
  completed: 'Processed and written',
  degraded: 'Written with optional steps skipped',
  failed_before_delivery: 'Processing failed before delivery',
  interrupted: 'Client write interrupted',
  upstream_failed: 'Upstream failed',
  overloaded: 'Processing capacity reached',
  unknown: 'Insufficient evidence',
}

export function AdaptationHealth(props: { channelId: number }) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const [opened, setOpened] = useState(false)
  const query = useQuery({
    queryKey: ['channel-adaptation-health', props.channelId],
    queryFn: () => getChannelAdaptationHealth(props.channelId),
    enabled: opened,
    refetchOnWindowFocus: false,
  })
  return (
    <section aria-label={t('Recent processing results')} className='space-y-3'>
      <Button
        type='button'
        variant='ghost'
        aria-expanded={opened}
        onClick={() => setOpened(!opened)}
      >
        {t('Recent processing results')}
      </Button>
      {opened && (
        <>
          <p className='text-muted-foreground text-sm'>
            {t(
              'Shows up to 50 completed recorded attempts from the last 24 hours, including retries. This sample is not the success rate of all traffic. Written means the server finished writing, not proof the client received it.'
            )}
          </p>
          {query.isPending && <LoadingState inline />}
          {query.isError && (
            <ErrorState
              title={t('Could not load processing results')}
              onRetry={() => void query.refetch()}
              className='min-h-0'
            />
          )}
          {query.data && (
            <>
              {!query.data.audit_enabled && (
                <p className='text-muted-foreground text-sm'>
                  {t(
                    'Request recording is off. Only previously retained records can appear here.'
                  )}
                </p>
              )}
              <p className='text-sm'>
                {t('Recorded attempts: {{count}}', {
                  count: query.data.sample_size,
                })}
              </p>
              <dl className='grid gap-x-6 gap-y-2 sm:grid-cols-2'>
                {Object.entries(labels).map(([key, label]) => (
                  <div
                    key={key}
                    className='flex min-w-0 justify-between gap-3 text-sm'
                  >
                    <dt>{t(label)}</dt>
                    <dd>
                      {formatNumber(query.data?.counts[key] ?? 0, locale)}
                    </dd>
                  </div>
                ))}
              </dl>
              {query.data.versions.length > 0 && (
                <details>
                  <summary className='cursor-pointer text-sm'>
                    {t('Separate results by configuration')}
                  </summary>
                  <div className='mt-2 space-y-3'>
                    {query.data.versions.map((version) => (
                      <div key={version.profile} className='text-sm'>
                        <p className='text-muted-foreground'>
                          {version.profile === 'unknown'
                            ? t('Unrecorded configuration')
                            : version.profile.slice(0, 12)}
                        </p>
                        {Object.entries(version.counts).map(([key, count]) => (
                          <p key={key}>
                            {t(
                              labels[key as keyof typeof labels] ??
                                'Insufficient evidence'
                            )}
                            : {formatNumber(count, locale)}
                          </p>
                        ))}
                      </div>
                    ))}
                  </div>
                </details>
              )}
              <Button
                type='button'
                variant='outline'
                disabled={query.isFetching}
                onClick={() => void query.refetch()}
              >
                {t('Refresh')}
              </Button>
            </>
          )}
        </>
      )}
    </section>
  )
}
