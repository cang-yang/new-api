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

import { CopyButton } from '@/components/copy-button'
import { Alert, AlertDescription } from '@/components/ui/alert'

import type { ParsedBodyAuditResponse } from '../lib/body-audit-response'
import { BodyAuditJsonContent } from './body-audit-json-content'

interface BodyAuditResultContentProps {
  result: ParsedBodyAuditResponse
  emptyLabel: string
  imageAlt: string
}

export function BodyAuditResultContent(props: BodyAuditResultContentProps) {
  const { t } = useTranslation()
  const tools = props.result.toolCalls ?? []
  const hasDetails =
    tools.length > 0 ||
    props.result.reasoningOnly ||
    (!!props.result.reasoning && !props.result.reasoningOnly) ||
    (props.result.warnings?.length ?? 0) > 0 ||
    (props.result.outputs?.length ?? 0) > 1
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
  if (hasDetails) {
    return (
      <div className='flex min-w-0 flex-col gap-4 [overflow-wrap:anywhere]'>
        {props.result.reasoningOnly && (
          <Alert>
            <AlertDescription>
              {t(
                'Only reasoning was returned; no answer or tool call was received.'
              )}
            </AlertDescription>
          </Alert>
        )}
        {props.result.warnings?.map((warning) => (
          <Alert key={warning}>
            <AlertDescription>{warningLabels[warning]}</AlertDescription>
          </Alert>
        ))}
        {(props.result.outputs?.length ?? 0) > 1
          ? props.result.outputs?.map((output) => (
              <section key={output.index} className='min-w-0'>
                <h4 className='mb-2 text-xs font-medium'>
                  {t('Response choice')} {output.index + 1}
                </h4>
                <div className='text-sm leading-7 whitespace-pre-wrap'>
                  {output.text}
                </div>
              </section>
            ))
          : props.result.text &&
            !props.result.reasoningOnly && (
              <div className='text-sm leading-7 whitespace-pre-wrap'>
                {props.result.text}
              </div>
            )}
        {tools.map((tool) => {
          return (
            <section key={tool.key} className='min-w-0 border-t pt-3'>
              <div className='mb-2 flex flex-wrap items-center justify-between gap-2'>
                <h4 className='min-w-0 text-sm font-medium'>
                  {tool.name || t('Tool call')}
                </h4>
                <CopyButton
                  value={tool.arguments}
                  size='sm'
                  tooltip={t('Copy tool arguments')}
                />
              </div>
              {!tool.valid && (
                <Alert>
                  <AlertDescription>
                    {t('Tool arguments are not valid JSON')}
                  </AlertDescription>
                </Alert>
              )}
              {tool.valid ? (
                <BodyAuditJsonContent value={JSON.parse(tool.arguments)} />
              ) : (
                <pre className='mt-2 font-mono text-xs leading-6 whitespace-pre-wrap'>
                  {tool.arguments}
                </pre>
              )}
            </section>
          )
        })}
        {props.result.reasoning && (
          <details open={props.result.reasoningOnly}>
            <summary className='cursor-pointer text-xs font-medium'>
              {t('Reasoning')}
            </summary>
            <div className='mt-2 text-sm leading-7 whitespace-pre-wrap'>
              {props.result.reasoning}
            </div>
          </details>
        )}
        {props.result.kind === 'structured' && (
          <details>
            <summary className='cursor-pointer text-xs'>
              {t('Original payload')}
            </summary>
            <pre className='mt-2 font-mono text-xs whitespace-pre-wrap'>
              {typeof props.result.structured === 'string'
                ? props.result.structured
                : JSON.stringify(props.result.structured, null, 2)}
            </pre>
          </details>
        )}
      </div>
    )
  }
  if (props.result.kind === 'text') {
    return (
      <div className='text-sm leading-7 break-words whitespace-pre-wrap'>
        {props.result.text}
      </div>
    )
  }

  if (props.result.kind === 'image') {
    return (
      <div className='grid gap-3 sm:grid-cols-2'>
        {props.result.media.map((item) => (
          <figure
            key={item.source}
            className='border-border bg-muted/20 overflow-hidden rounded-lg border'
          >
            <a href={item.source} target='_blank' rel='noreferrer'>
              <img
                src={item.source}
                alt={item.label || props.imageAlt}
                loading='lazy'
                className='max-h-96 w-full bg-black/5 object-contain dark:bg-white/5'
              />
            </a>
            {item.label && (
              <figcaption className='text-muted-foreground border-t px-3 py-2 text-xs leading-5'>
                {item.label}
              </figcaption>
            )}
          </figure>
        ))}
      </div>
    )
  }

  if (props.result.kind === 'audio') {
    return (
      <div className='space-y-3'>
        {props.result.media.map((item) => (
          <audio
            key={item.source}
            controls
            preload='metadata'
            src={item.source}
            className='w-full'
          />
        ))}
      </div>
    )
  }

  if (props.result.kind === 'video') {
    return (
      <div className='space-y-3'>
        {props.result.media.map((item) => (
          <video
            key={item.source}
            controls
            preload='metadata'
            src={item.source}
            className='bg-muted/20 max-h-[440px] w-full rounded-lg'
          />
        ))}
      </div>
    )
  }

  if (props.result.kind === 'structured') {
    const formatted =
      typeof props.result.structured === 'string'
        ? props.result.structured
        : JSON.stringify(props.result.structured, null, 2)
    return (
      <pre className='font-mono text-xs leading-6 break-words whitespace-pre-wrap'>
        {formatted}
      </pre>
    )
  }

  return <div className='text-muted-foreground text-sm'>{props.emptyLabel}</div>
}
