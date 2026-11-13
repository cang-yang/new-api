import type { Channel } from '../types'
import {
  transformChannelToFormDefaults,
  transformFormDataToUpdatePayload,
  type ChannelFormValues,
} from './channel-form'

export const adaptationKeys = [
  'adaptation_scope',
  'sillytavern_preset',
  'worldbook',
  'response_text_filter',
] as const

export type AdaptationBundle = Partial<
  Record<(typeof adaptationKeys)[number], unknown>
>
export type AdaptationState = {
  revision: number
  active_revision: number
  active: AdaptationBundle
  draft?: AdaptationBundle | null
  previous?: AdaptationBundle | null
  known_good?: AdaptationBundle | null
  checked: boolean
  base_digest: string
}
export type AdaptationAction =
  | 'save_draft'
  | 'check'
  | 'activate'
  | 'restore_previous'
  | 'restore_known_good'
  | 'mark_known_good'

export function pickAdaptationBundle(settings?: string): AdaptationBundle {
  const parsed = JSON.parse(settings || '{}') as Record<string, unknown>
  return Object.fromEntries(
    adaptationKeys
      .filter((key) => key in parsed)
      .map((key) => [key, parsed[key]])
  )
}

export function adaptationBundleFromForm(
  values: ChannelFormValues
): AdaptationBundle {
  return pickAdaptationBundle(
    transformFormDataToUpdatePayload(values, 0).settings
  )
}

export function adaptationBundleSignature(bundle: AdaptationBundle): string {
  return JSON.stringify(bundle, (_key, value: unknown) => {
    if (value !== null && typeof value === 'object' && !Array.isArray(value)) {
      const record = value as Record<string, unknown>
      return Object.fromEntries(
        Object.keys(record)
          .sort()
          .map((key) => [key, record[key]])
      )
    }
    return value
  })
}

export function adaptationFormFields(
  bundle: AdaptationBundle,
  channel: Channel
) {
  const values = transformChannelToFormDefaults({
    ...channel,
    settings: JSON.stringify(bundle),
  })
  return {
    adaptation_scope: values.adaptation_scope,
    worldbook: values.worldbook,
    response_text_filter: values.response_text_filter,
    sillytavern_preset: values.sillytavern_preset,
    sillytavern_preset_patches: values.sillytavern_preset_patches,
    sillytavern_user: values.sillytavern_user,
    sillytavern_char: values.sillytavern_char,
    sillytavern_models: values.sillytavern_models,
    sillytavern_parameter_policy: values.sillytavern_parameter_policy,
    sillytavern_post_processing: values.sillytavern_post_processing,
    sillytavern_reference_source: values.sillytavern_reference_source,
    sillytavern_context_mode: values.sillytavern_context_mode,
  }
}

export function retainActiveAdaptation(
  settings: string,
  active: AdaptationBundle
): string {
  const result = JSON.parse(settings) as Record<string, unknown>
  for (const key of adaptationKeys) {
    delete result[key]
    if (key in active) result[key] = active[key]
  }
  return JSON.stringify(result)
}
