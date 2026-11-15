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

import { Button } from '@/components/ui/button'
import { handleServerError } from '@/lib/handle-server-error'

import { downloadConfiguration } from '../lib/config-export'

export function ConfigurationExportButton(props: {
  label: string
  filename: string
  value?: unknown
  getValue?: () => unknown
  disabled?: boolean
}) {
  const { t } = useTranslation()
  return (
    <Button
      type='button'
      variant='outline'
      disabled={props.disabled || (props.value == null && !props.getValue)}
      onClick={() => {
        try {
          downloadConfiguration(
            props.getValue ? props.getValue() : props.value,
            props.filename
          )
        } catch (error) {
          handleServerError(error)
        }
      }}
    >
      {t(props.label)}
    </Button>
  )
}
