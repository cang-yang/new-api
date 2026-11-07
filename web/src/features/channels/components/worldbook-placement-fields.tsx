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
import { useId } from 'react'
import { useTranslation } from 'react-i18next'

import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'

import type { WorldbookEntry } from '../lib/worldbook'
import { worldbookPositionLabels } from '../lib/worldbook-placement'

export function WorldbookPlacementFields(props: {
  entry: WorldbookEntry
  disabled?: boolean
  onChange: (entry: WorldbookEntry) => void
}) {
  const { t } = useTranslation()
  const id = useId()
  return (
    <div className='grid gap-3 sm:grid-cols-2'>
      <div className='space-y-2'>
        <Label htmlFor={`${id}-position`}>
          {t('Entry insertion position')}
        </Label>
        <Select
          value={props.entry.injection_mode || 'follow_global'}
          disabled={props.disabled}
          onValueChange={(value) => {
            if (value) {
              props.onChange({
                ...props.entry,
                injection_mode: value as WorldbookEntry['injection_mode'],
              })
            }
          }}
        >
          <SelectTrigger id={`${id}-position`} className='w-full'>
            <SelectValue />
          </SelectTrigger>
          <SelectContent alignItemWithTrigger={false}>
            {Object.entries(worldbookPositionLabels).map(([value, label]) => (
              <SelectItem key={value} value={value}>
                {t(label)}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <p className='text-muted-foreground text-xs'>
          {t(
            'Applied before preset scripts. Scripts may reorganize the messages afterward.'
          )}
        </p>
      </div>
      {props.entry.injection_mode === 'at_depth' && (
        <div className='space-y-2'>
          <Label htmlFor={`${id}-depth`}>{t('Conversation depth')}</Label>
          <Input
            id={`${id}-depth`}
            type='number'
            min={0}
            max={20}
            value={props.entry.injection_depth ?? 0}
            onChange={(event) =>
              props.onChange({
                ...props.entry,
                injection_depth: Number(event.target.value),
              })
            }
          />
          <p className='text-muted-foreground text-xs'>
            {t(
              'Depth 0 is after the latest message; depth 1 is before it. Tool exchanges stay together.'
            )}
          </p>
        </div>
      )}
    </div>
  )
}
