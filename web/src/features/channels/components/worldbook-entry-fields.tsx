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
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'

import type { WorldbookEntry } from '../lib/worldbook'

export function WorldbookEntryFields(props: {
  entry: WorldbookEntry
  onChange: (entry: WorldbookEntry) => void
  disabled?: boolean
}) {
  const { t } = useTranslation()
  const id = useId()
  const [simplify, setSimplify] = useState(false)
  const [keys, setKeys] = useState(props.entry.keys.join('\n'))
  const [secondary, setSecondary] = useState(
    props.entry.secondary_keys.join('\n')
  )
  const blocked = !!props.entry.unsupported?.length
  const update = (patch: Partial<WorldbookEntry>) =>
    props.onChange({ ...props.entry, ...patch })
  return (
    <fieldset disabled={props.disabled} className='min-w-0 space-y-4'>
      {blocked && (
        <div
          role='status'
          className='bg-muted/40 space-y-2 rounded-lg border p-3 text-sm'
        >
          <p>{t('This entry has unsupported conditions and will not run.')}</p>
          <p className='text-muted-foreground text-xs break-words'>
            {props.entry.unsupported?.join(' · ')}
          </p>
          <Button
            type='button'
            size='sm'
            variant='outline'
            onClick={() => setSimplify(true)}
          >
            {t('Use supported conditions only')}
          </Button>
        </div>
      )}
      <div className='flex flex-wrap items-center justify-between gap-3'>
        <Label htmlFor={`${id}-enabled`}>{t('Enable entry')}</Label>
        <Switch
          id={`${id}-enabled`}
          checked={props.entry.enabled}
          disabled={blocked || props.disabled}
          onCheckedChange={(enabled) => update({ enabled })}
        />
      </div>
      <div className='space-y-2'>
        <Label htmlFor={`${id}-name`}>{t('Entry name')}</Label>
        <Input
          id={`${id}-name`}
          value={props.entry.name}
          onChange={(event) => update({ name: event.target.value })}
        />
      </div>
      <div className='grid gap-4 sm:grid-cols-2'>
        <div className='space-y-2'>
          <Label htmlFor={`${id}-trigger`}>{t('When to include')}</Label>
          <NativeSelect
            id={`${id}-trigger`}
            className='w-full'
            value={props.entry.constant ? 'always' : 'keywords'}
            onChange={(event) =>
              update({ constant: event.target.value === 'always' })
            }
          >
            <NativeSelectOption value='keywords'>
              {t('When keywords match')}
            </NativeSelectOption>
            <NativeSelectOption value='always'>
              {t('Always include')}
            </NativeSelectOption>
          </NativeSelect>
        </div>
        <div className='space-y-2'>
          <Label htmlFor={`${id}-priority`}>
            {t('Priority (higher first)')}
          </Label>
          <Input
            id={`${id}-priority`}
            type='number'
            value={props.entry.priority}
            onChange={(event) =>
              update({ priority: Number(event.target.value) })
            }
          />
        </div>
      </div>
      {!props.entry.constant && (
        <>
          <div className='space-y-2'>
            <Label htmlFor={`${id}-keys`}>{t('Trigger keywords')}</Label>
            <Textarea
              id={`${id}-keys`}
              rows={3}
              value={keys}
              onChange={(event) => {
                setKeys(event.target.value)
                update({
                  keys: event.target.value
                    .split('\n')
                    .map((key) => key.trim())
                    .filter(Boolean),
                })
              }}
            />
            <p className='text-muted-foreground text-xs'>
              {t(
                'One keyword per line. Any primary keyword can match. Use /pattern/i for a supported regular expression.'
              )}
            </p>
          </div>
          <div className='flex flex-wrap gap-5'>
            <Label htmlFor={`${id}-case`} className='gap-3'>
              <Switch
                id={`${id}-case`}
                checked={props.entry.case_sensitive}
                onCheckedChange={(case_sensitive) => update({ case_sensitive })}
              />
              {t('Case sensitive')}
            </Label>
            <Label htmlFor={`${id}-whole`} className='gap-3'>
              <Switch
                id={`${id}-whole`}
                checked={props.entry.whole_words}
                onCheckedChange={(whole_words) => update({ whole_words })}
              />
              {t('Match whole words')}
            </Label>
          </div>
          <details className='rounded-lg border p-3'>
            <summary className='cursor-pointer text-sm font-medium'>
              {t('Additional keyword conditions')}
            </summary>
            <div className='mt-3 space-y-3'>
              <NativeSelect
                aria-label={t('Additional keyword conditions')}
                className='w-full'
                value={props.entry.secondary_logic}
                onChange={(event) =>
                  update({
                    secondary_logic: event.target
                      .value as WorldbookEntry['secondary_logic'],
                  })
                }
              >
                <NativeSelectOption value='none'>
                  {t('No additional condition')}
                </NativeSelectOption>
                <NativeSelectOption value='any'>
                  {t('Also match any of these')}
                </NativeSelectOption>
                <NativeSelectOption value='all'>
                  {t('Also match all of these')}
                </NativeSelectOption>
                <NativeSelectOption value='not_any'>
                  {t('Must not match any of these')}
                </NativeSelectOption>
                <NativeSelectOption value='not_all'>
                  {t('Must not match all of these')}
                </NativeSelectOption>
              </NativeSelect>
              <Textarea
                aria-label={t('Additional keywords')}
                rows={3}
                value={secondary}
                onChange={(event) => {
                  setSecondary(event.target.value)
                  update({
                    secondary_keys: event.target.value
                      .split('\n')
                      .map((key) => key.trim())
                      .filter(Boolean),
                  })
                }}
              />
            </div>
          </details>
        </>
      )}
      <div className='space-y-2'>
        <Label htmlFor={`${id}-content`}>{t('Entry content')}</Label>
        <Textarea
          id={`${id}-content`}
          rows={8}
          className='min-h-40 resize-y leading-relaxed'
          value={props.entry.content}
          onChange={(event) => update({ content: event.target.value })}
        />
      </div>
      <ConfirmDialog
        open={simplify}
        onOpenChange={setSimplify}
        title={t('Remove unsupported conditions?')}
        desc={t(
          'This changes when the entry can activate. Review the remaining keywords before enabling it. The original imported data is kept.'
        )}
        confirmText={t('Use supported conditions only')}
        handleConfirm={() => {
          update({ unsupported: [], enabled: false })
          setSimplify(false)
        }}
      />
    </fieldset>
  )
}
