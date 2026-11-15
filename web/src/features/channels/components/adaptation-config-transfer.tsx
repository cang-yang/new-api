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
import { useFormContext, useWatch } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'

import {
  adaptationBundleFromForm,
  adaptationFormFields,
} from '../lib/adaptation-lifecycle'
import type { ChannelFormValues } from '../lib/channel-form'
import { channelSchema, type Channel } from '../types'
import { AdaptationPackageControls } from './adaptation-package-controls'

export function AdaptationConfigTransfer(props: {
  channel?: Channel
  disabled?: boolean
}) {
  const { t } = useTranslation()
  const form = useFormContext<ChannelFormValues>()
  const values = useWatch({ control: form.control }) as ChannelFormValues
  // Invalid edits must remain editable, but cannot be exported as a valid pack.
  let bundle = null
  try {
    bundle = adaptationBundleFromForm(values)
  } catch {
    // The individual form fields display their validation errors.
  }
  return (
    <Card>
      <CardHeader>
        <CardTitle>{t('Configuration import and export')}</CardTitle>
        <CardDescription>
          {t(
            'Edit settings below, then click Save to apply them together. No separate activation is needed.'
          )}
        </CardDescription>
      </CardHeader>
      <CardContent className='flex flex-col gap-3'>
        <AdaptationPackageControls
          bundle={bundle}
          disabled={props.disabled}
          onImport={(next) => {
            const channel =
              props.channel ??
              channelSchema.parse({
                id: 0,
                name: '',
                type: 1,
                key: '',
                status: 1,
                created_time: 0,
                test_time: 0,
                response_time: 0,
                balance_updated_time: 0,
                models: '',
              })
            const fields = adaptationFormFields(next, channel)
            for (const key of Object.keys(fields) as Array<
              keyof typeof fields
            >) {
              form.setValue(key, fields[key], {
                shouldDirty: true,
                shouldValidate: true,
              })
            }
          }}
        />
        <p className='text-muted-foreground text-xs'>
          {t(
            'Exports include the current preset, scripts, worldbooks and rules, including unsaved edits. Channel keys and connection settings are not included. Keep exported files private.'
          )}
        </p>
      </CardContent>
    </Card>
  )
}
