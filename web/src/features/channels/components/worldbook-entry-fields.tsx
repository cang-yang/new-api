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

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'

import {
  worldbookConfirmEntry,
  worldbookEntryBlocked,
  worldbookRiskFields,
  type WorldbookEntry,
} from '../lib/worldbook'
import { WorldbookEntryNotes } from './worldbook-import-summary'
import { WorldbookPlacementFields } from './worldbook-placement-fields'

export function WorldbookEntryFields(props: {
  entry: WorldbookEntry
  onChange: (entry: WorldbookEntry) => void
  disabled?: boolean
}) {
  const { t } = useTranslation()
  const id = useId()
  const [keys, setKeys] = useState(props.entry.keys.join('\n'))
  const [secondary, setSecondary] = useState(
    props.entry.secondary_keys.join('\n')
  )
  const invalid = worldbookEntryBlocked(props.entry)
  const risk = worldbookRiskFields(props.entry)
  const update = (patch: Partial<WorldbookEntry>) =>
    props.onChange({ ...props.entry, ...patch })
  return (
    <fieldset disabled={props.disabled} className='min-w-0 space-y-4'>
      {!!risk.length && !invalid && (
        <div
          role='alert'
          className='space-y-2 rounded-lg border border-amber-500/40 bg-amber-500/5 p-3 text-sm'
        >
          <p>
            {t(
              'This entry was imported disabled because it uses behavior the gateway cannot reproduce: {{fields}}.',
              { fields: risk.join(', ') }
            )}
          </p>
          {!props.entry.enabled && (
            <Button
              type='button'
              variant='outline'
              size='sm'
              disabled={props.disabled}
              onClick={() => props.onChange(worldbookConfirmEntry(props.entry))}
            >
              {t('Confirm and enable')}
            </Button>
          )}
        </div>
      )}
      {invalid && (
        <div
          role='alert'
          className='border-destructive/30 bg-destructive/5 space-y-2 rounded-lg border p-3 text-sm'
        >
          <p>{t('Invalid data; this entry will not run.')}</p>
          <p className='text-muted-foreground text-xs break-words'>
            {props.entry.issues?.join(' · ')}
          </p>
        </div>
      )}
      <WorldbookEntryNotes entry={props.entry} />
      <div className='flex flex-wrap items-center justify-between gap-3'>
        <Label htmlFor={`${id}-enabled`}>{t('Enable entry')}</Label>
        <Switch
          id={`${id}-enabled`}
          checked={props.entry.enabled}
          disabled={props.disabled}
          onCheckedChange={(enabled) => {
            if (enabled && invalid) return
            update({ enabled })
          }}
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
          <Select
            value={props.entry.constant ? 'always' : 'keywords'}
            onValueChange={(value) => update({ constant: value === 'always' })}
            disabled={props.disabled}
          >
            <SelectTrigger id={`${id}-trigger`} className='w-full'>
              <SelectValue />
            </SelectTrigger>
            <SelectContent alignItemWithTrigger={false}>
              <SelectItem value='keywords'>
                {t('When keywords match')}
              </SelectItem>
              <SelectItem value='always'>{t('Always include')}</SelectItem>
            </SelectContent>
          </Select>
        </div>
        <div className='space-y-2'>
          <Label htmlFor={`${id}-priority`}>
            {t('Priority (higher near the end)')}
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
      <WorldbookPlacementFields
        entry={props.entry}
        onChange={props.onChange}
        disabled={props.disabled}
      />
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
              <Select
                value={props.entry.secondary_logic}
                onValueChange={(value) =>
                  update({
                    secondary_logic: value as WorldbookEntry['secondary_logic'],
                  })
                }
                disabled={props.disabled}
              >
                <SelectTrigger
                  aria-label={t('Additional keyword conditions')}
                  className='w-full'
                >
                  <SelectValue />
                </SelectTrigger>
                <SelectContent alignItemWithTrigger={false}>
                  <SelectItem value='none'>
                    {t('No additional condition')}
                  </SelectItem>
                  <SelectItem value='any'>
                    {t('Also match any of these')}
                  </SelectItem>
                  <SelectItem value='all'>
                    {t('Also match all of these')}
                  </SelectItem>
                  <SelectItem value='not_any'>
                    {t('Must not match any of these')}
                  </SelectItem>
                  <SelectItem value='not_all'>
                    {t('Must not match all of these')}
                  </SelectItem>
                </SelectContent>
              </Select>
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
      <div className='flex flex-wrap items-center justify-between gap-3 rounded-lg border p-3'>
        <div className='space-y-1'>
          <Label htmlFor={`${id}-prevent-recursion`}>
            {t('Prevent recursive matching')}
          </Label>
          <p className='text-muted-foreground text-xs'>
            {t('This entry cannot trigger additional entries.')}
          </p>
        </div>
        <Switch
          id={`${id}-prevent-recursion`}
          checked={!!props.entry.prevent_recursion}
          disabled={props.disabled}
          onCheckedChange={(prevent_recursion) => update({ prevent_recursion })}
        />
      </div>
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
    </fieldset>
  )
}
