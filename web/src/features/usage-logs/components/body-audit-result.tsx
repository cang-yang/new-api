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
import type { ParsedBodyAuditResponse } from '../lib/body-audit-response'

interface BodyAuditResultContentProps {
  result: ParsedBodyAuditResponse
  emptyLabel: string
  imageAlt: string
}

export function BodyAuditResultContent(props: BodyAuditResultContentProps) {
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
