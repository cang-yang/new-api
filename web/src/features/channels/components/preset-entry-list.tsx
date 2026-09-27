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
import {
  DndContext,
  DragOverlay,
  KeyboardSensor,
  PointerSensor,
  closestCenter,
  useSensor,
  useSensors,
} from '@dnd-kit/core'
import {
  SortableContext,
  sortableKeyboardCoordinates,
  useSortable,
  verticalListSortingStrategy,
} from '@dnd-kit/sortable'
import { CSS } from '@dnd-kit/utilities'
import { DragDropVerticalIcon } from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import { useId, useState } from 'react'
import { createPortal } from 'react-dom'
import { useTranslation } from 'react-i18next'

import { EmptyState } from '@/components/empty-state'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import { cn } from '@/lib/utils'

import type { PresetEntry } from '../lib/sillytavern-editor'

type Props = {
  entries: PresetEntry[]
  selected: string
  search: string
  disabled?: boolean
  canRestore: boolean
  onSearch: (value: string) => void
  onSelect: (identifier: string) => void
  onToggle: (identifiers: string[], enabled: boolean) => void
  onRestore: () => void
  onReorder: (identifier: string, target: string) => void
  onDragging: (active: boolean) => void
}

export function PresetEntryList(props: Props) {
  const { t } = useTranslation()
  const instructionsId = useId()
  const [active, setActive] = useState<string | null>(null)
  const [over, setOver] = useState<string | null>(null)
  const query = props.search.trim().toLocaleLowerCase()
  const entries = props.entries.filter((entry) =>
    `${entry.name} ${entry.identifier}`.toLocaleLowerCase().includes(query)
  )
  const sortingDisabled = !!props.disabled || !!query || entries.length < 2
  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 6 } }),
    useSensor(KeyboardSensor, { coordinateGetter: sortableKeyboardCoordinates })
  )
  const activeEntry = props.entries.find((entry) => entry.identifier === active)
  const activeIndex = props.entries.findIndex(
    (entry) => entry.identifier === active
  )
  const overIndex = props.entries.findIndex(
    (entry) => entry.identifier === over
  )
  const insertionSide = activeIndex < overIndex ? 'after' : 'before'
  const instructions = t(
    'Drag a handle to reorder. With a keyboard, press Space to grab, arrow keys to move, Space to drop, or Escape to cancel.'
  )
  return (
    <section
      aria-label={t('Preset entry list')}
      className='flex h-full min-h-0 flex-col'
    >
      <div className='flex shrink-0 flex-col gap-3 border-b p-4'>
        <Input
          value={props.search}
          onChange={(event) => props.onSearch(event.target.value)}
          aria-label={t('Search preset entries')}
          placeholder={t('Search preset entries')}
        />
        <div className='flex flex-wrap gap-1'>
          <Button
            type='button'
            variant='outline'
            size='sm'
            disabled={props.disabled || entries.length === 0}
            onClick={() =>
              props.onToggle(
                entries.map((entry) => entry.identifier),
                true
              )
            }
          >
            {t('Enable visible entries')}
          </Button>
          <Button
            type='button'
            variant='outline'
            size='sm'
            disabled={props.disabled || entries.length === 0}
            onClick={() =>
              props.onToggle(
                entries.map((entry) => entry.identifier),
                false
              )
            }
          >
            {t('Disable visible entries')}
          </Button>
        </div>
        <p
          id={instructionsId}
          className='text-muted-foreground text-xs leading-relaxed'
        >
          {query ? t('Clear search to reorder entries.') : instructions}
        </p>
      </div>
      <DndContext
        sensors={sensors}
        collisionDetection={closestCenter}
        autoScroll
        accessibility={{
          screenReaderInstructions: { draggable: instructions },
          announcements: {
            onDragStart: ({ active: item }) =>
              t('Picked up {{name}}.', {
                name: props.entries.find(
                  (entry) => entry.identifier === item.id
                )?.name,
              }),
            onDragOver: ({ over: item }) =>
              item
                ? t('Move to position {{position}}.', {
                    position:
                      props.entries.findIndex(
                        (entry) => entry.identifier === item.id
                      ) + 1,
                  })
                : undefined,
            onDragEnd: () => t('Entry order updated.'),
            onDragCancel: () => t('Reordering cancelled.'),
          },
        }}
        onDragStart={(event) => {
          setActive(String(event.active.id))
          setOver(String(event.active.id))
          props.onDragging(true)
        }}
        onDragOver={(event) =>
          setOver(event.over ? String(event.over.id) : null)
        }
        onDragCancel={() => {
          setActive(null)
          setOver(null)
          props.onDragging(false)
        }}
        onDragEnd={(event) => {
          if (!sortingDisabled && event.over) {
            props.onReorder(String(event.active.id), String(event.over.id))
          }
          setActive(null)
          setOver(null)
          props.onDragging(false)
        }}
      >
        <div className='min-h-0 flex-1 overflow-y-auto overscroll-contain p-2'>
          {entries.length === 0 ? (
            <EmptyState
              className='min-h-40'
              title={
                props.entries.length
                  ? t('No matching preset entries')
                  : t('No preset entries')
              }
            />
          ) : (
            <SortableContext
              items={entries.map((entry) => entry.identifier)}
              strategy={verticalListSortingStrategy}
            >
              <ul
                aria-label={t('Preset entries')}
                className='flex flex-col gap-1'
              >
                {entries.map((entry) => (
                  <PresetEntryRow
                    key={entry.identifier}
                    entry={entry}
                    selected={props.selected === entry.identifier}
                    disabled={props.disabled}
                    sortingDisabled={sortingDisabled}
                    instructionsId={instructionsId}
                    onSelect={props.onSelect}
                    onToggle={props.onToggle}
                    insertion={
                      over === entry.identifier && active !== over
                        ? insertionSide
                        : undefined
                    }
                  />
                ))}
              </ul>
            </SortableContext>
          )}
        </div>
        {createPortal(
          <DragOverlay dropAnimation={null}>
            {activeEntry ? (
              <div className='bg-popover text-popover-foreground truncate rounded-lg border px-4 py-3 text-sm shadow-lg'>
                {activeEntry.name}
              </div>
            ) : null}
          </DragOverlay>,
          document.body
        )}
      </DndContext>
      <div className='shrink-0 border-t px-4 py-2'>
        <Button
          type='button'
          variant='ghost'
          size='sm'
          disabled={props.disabled || !props.canRestore}
          onClick={props.onRestore}
        >
          {t('Restore entry switches')}
        </Button>
      </div>
    </section>
  )
}

function PresetEntryRow(props: {
  entry: PresetEntry
  selected: boolean
  disabled?: boolean
  sortingDisabled: boolean
  instructionsId: string
  insertion?: 'before' | 'after'
  onSelect: Props['onSelect']
  onToggle: Props['onToggle']
}) {
  const { t } = useTranslation()
  const sortable = useSortable({
    id: props.entry.identifier,
    disabled: props.sortingDisabled,
  })
  const name = props.entry.name || props.entry.identifier
  return (
    <li
      ref={sortable.setNodeRef}
      style={{
        transform: CSS.Transform.toString(sortable.transform),
        transition: sortable.transition,
      }}
      className={cn(
        'relative flex min-w-0 items-center gap-1 rounded-lg border border-transparent px-1',
        props.selected && 'bg-accent border-border',
        sortable.isDragging && 'opacity-30'
      )}
    >
      {props.insertion && (
        <span
          aria-hidden='true'
          className={cn(
            'bg-primary pointer-events-none absolute inset-x-0 h-0.5',
            props.insertion === 'before' ? '-top-0.5' : '-bottom-0.5'
          )}
        />
      )}
      <Button
        ref={sortable.setActivatorNodeRef}
        type='button'
        variant='ghost'
        size='icon-sm'
        className='shrink-0 cursor-grab touch-none active:cursor-grabbing'
        {...sortable.attributes}
        {...sortable.listeners}
        disabled={props.sortingDisabled}
        aria-describedby={props.instructionsId}
        aria-label={t('Reorder {{name}}', { name })}
      >
        <HugeiconsIcon icon={DragDropVerticalIcon} aria-hidden='true' />
      </Button>
      <Button
        type='button'
        variant='ghost'
        className='h-auto min-w-0 flex-1 justify-start px-1 py-3 text-start whitespace-normal'
        aria-pressed={props.selected}
        onClick={() => props.onSelect(props.entry.identifier)}
      >
        <span className='flex min-w-0 flex-col gap-1'>
          <span className='line-clamp-2 [overflow-wrap:anywhere] break-words'>
            {name}
          </span>
          <span className='text-muted-foreground text-xs font-normal'>
            {props.entry.marker ? t('Context marker') : props.entry.role}
          </span>
        </span>
      </Button>
      <Switch
        className='shrink-0'
        checked={props.entry.enabled}
        disabled={props.disabled}
        aria-label={t('Enable preset entry {{name}}', {
          name,
        })}
        onCheckedChange={(checked) =>
          props.onToggle([props.entry.identifier], checked)
        }
      />
    </li>
  )
}
