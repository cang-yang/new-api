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

import { CopyButton } from '@/components/copy-button'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'

import type { ParsedBodyAuditResponse } from '../lib/body-audit-response'
import { AuditText, BodyAuditJsonContent } from './body-audit-json-content'

interface BodyAuditResultContentProps {
  result: ParsedBodyAuditResponse
  emptyLabel: string
  imageAlt: string
}

export function BodyAuditResultContent(props: BodyAuditResultContentProps) {
  const { t } = useTranslation()
  const [selected, setSelected] = useState('')
  const tools = props.result.toolCalls ?? []
  const outputs = props.result.outputs?.length
    ? props.result.outputs
    : [
        {
          index: 0,
          text: props.result.reasoningOnly ? '' : props.result.text,
          reasoning: props.result.reasoning || '',
        },
      ]
  const sections = [
    ...outputs
      .filter((output) => output.text)
      .map((output) => ({
        key: `answer-${output.index}`,
        label:
          outputs.length > 1
            ? `${t('Response choice')} ${output.index + 1}`
            : t('Answer'),
        text: output.text,
        tool: null,
      })),
    ...tools.map((tool, index) => ({
      key: `tool-${tool.key}`,
      label: `${t('Tool call')} ${index + 1} · ${tool.name || tool.id}`,
      text: tool.arguments,
      tool,
    })),
    ...outputs
      .filter((output) => output.reasoning)
      .map((output) => ({
        key: `reasoning-${output.index}`,
        label:
          outputs.length > 1
            ? `${t('Reasoning')} · ${output.index + 1}`
            : t('Reasoning'),
        text: output.reasoning,
        tool: null,
      })),
  ]
  const active =
    sections.find((section) => section.key === selected) ?? sections[0]
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
        {active && (
          <section className='min-w-0 space-y-4'>
            <div className='bg-background sticky top-0 z-10 flex min-w-0 items-center justify-between gap-2 pb-2'>
              <NativeSelect
                aria-label={t('Reading section')}
                value={active.key}
                onChange={(event) => setSelected(event.target.value)}
                className='max-w-full min-w-0 flex-1'
              >
                {sections.map((section) => (
                  <NativeSelectOption key={section.key} value={section.key}>
                    {section.label}
                  </NativeSelectOption>
                ))}
              </NativeSelect>
              <CopyButton
                value={active.text}
                size='sm'
                tooltip={
                  active.tool
                    ? t('Copy tool arguments')
                    : t('Copy readable content')
                }
              />
            </div>
            {active.tool && (
              <p className='text-muted-foreground text-xs'>
                {t('Tool call arguments, not an execution result.')}
              </p>
            )}
            {active.tool && !active.tool.valid && (
              <Alert>
                <AlertDescription>
                  {t('Tool arguments are not valid JSON')}
                </AlertDescription>
              </Alert>
            )}
            {active.tool?.valid ? (
              <BodyAuditJsonContent
                key={active.key}
                value={JSON.parse(active.tool.arguments)}
              />
            ) : (
              <AuditText
                key={active.key}
                text={active.text}
                code={!!active.tool}
              />
            )}
          </section>
        )}
        {!active && props.result.kind === 'structured' && (
          <BodyAuditJsonContent value={props.result.structured} />
        )}
      </div>
    )
  }
  if (props.result.kind === 'text') {
    return <AuditText text={props.result.text} />
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
    return <BodyAuditJsonContent value={props.result.structured} />
  }

  return <div className='text-muted-foreground text-sm'>{props.emptyLabel}</div>
}
