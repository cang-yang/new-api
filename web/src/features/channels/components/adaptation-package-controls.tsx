import { useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { Button } from '@/components/ui/button'
import { handleServerError } from '@/lib/handle-server-error'

import {
  adaptationKeys,
  type AdaptationBundle,
} from '../lib/adaptation-lifecycle'

export function AdaptationPackageControls(props: {
  active: AdaptationBundle
  disabled?: boolean
  onImport: (bundle: AdaptationBundle) => void
}) {
  const { t } = useTranslation()
  const input = useRef<HTMLInputElement>(null)
  const [candidate, setCandidate] = useState<AdaptationBundle | null>(null)
  return (
    <>
      <div className='flex flex-wrap gap-2'>
        <Button
          type='button'
          variant='outline'
          disabled={props.disabled}
          onClick={() => input.current?.click()}
        >
          {t('Import complete adaptation package')}
        </Button>
        <input
          ref={input}
          type='file'
          accept='.json,application/json'
          className='hidden'
          aria-label={t('Adaptation package file')}
          disabled={props.disabled}
          onChange={async (event) => {
            const file = event.target.files?.[0]
            event.target.value = ''
            if (!file) return
            try {
              if (file.size > 16 * 1024 * 1024) {
                throw new Error(t('Adaptation package exceeds 16 MiB.'))
              }
              const value: unknown = JSON.parse(await file.text())
              if (!value || typeof value !== 'object' || Array.isArray(value)) {
                throw new Error(t('Invalid adaptation package.'))
              }
              const data = value as Record<string, unknown>
              if (
                data.format !== 'new_api_adaptation' ||
                data.version !== 1 ||
                !data.bundle ||
                typeof data.bundle !== 'object' ||
                Array.isArray(data.bundle) ||
                Object.keys(data.bundle).some(
                  (key) => !(adaptationKeys as readonly string[]).includes(key)
                )
              ) {
                throw new Error(t('Invalid adaptation package.'))
              }
              setCandidate(data.bundle as AdaptationBundle)
            } catch (error) {
              handleServerError(error)
            }
          }}
        />
        <Button
          type='button'
          variant='ghost'
          disabled={props.disabled}
          onClick={() => {
            const bundle = Object.fromEntries(
              adaptationKeys
                .filter((key) => key in props.active)
                .map((key) => [key, props.active[key]])
            )
            const url = URL.createObjectURL(
              new Blob(
                [
                  JSON.stringify(
                    { format: 'new_api_adaptation', version: 1, bundle },
                    null,
                    2
                  ),
                ],
                { type: 'application/json' }
              )
            )
            const link = document.createElement('a')
            link.href = url
            link.download = 'adaptation-config.json'
            link.click()
            URL.revokeObjectURL(url)
          }}
        >
          {t('Export active adaptation package')}
        </Button>
      </div>
      <ConfirmDialog
        open={candidate !== null}
        onOpenChange={(open) => {
          if (!open) setCandidate(null)
        }}
        title={t('Replace the adaptation draft?')}
        desc={t(
          'This imports the preset, scripts, worldbooks and rules together. Channel credentials and routing stay unchanged. Nothing runs or becomes active until you check and activate the draft. Only import files you trust.'
        )}
        handleConfirm={() => {
          if (!candidate) return
          try {
            props.onImport(candidate)
            setCandidate(null)
          } catch (error) {
            handleServerError(error)
          }
        }}
      />
    </>
  )
}
