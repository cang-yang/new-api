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
import { expect, test } from 'vitest'

import {
  getChannelConfigurationSection,
  getChannelConfigurationState,
} from '../channel-configuration'
import { CHANNEL_FORM_DEFAULT_VALUES } from '../channel-form'

test('invalid preset patches mark the request section and override block as errors', () => {
  expect(getChannelConfigurationSection('sillytavern_preset_patches')).toBe(
    'request'
  )
  const state = getChannelConfigurationState(
    CHANNEL_FORM_DEFAULT_VALUES,
    {
      sillytavern_preset_patches: {
        type: 'custom',
        message: 'Invalid patches',
      },
    },
    true
  )
  expect(state.sections.request).toBe('error')
  expect(state.blocks.overrideRules).toBe('error')
})
