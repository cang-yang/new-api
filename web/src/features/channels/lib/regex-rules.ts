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
import { z } from 'zod'

export const regexRuleSchema = z
  .object({
    id: z.string().min(1).max(128),
    name: z.string().max(256).optional(),
    disabled: z.boolean().optional(),
    stage: z.enum(['receive', 'send']),
    action: z.enum(['replace', 'extract']),
    pattern: z
      .string()
      .refine((value) => new TextEncoder().encode(value).length <= 16384),
    replacement: z
      .string()
      .refine((value) => new TextEncoder().encode(value).length <= 524288),
    roles: z
      .array(z.enum(['user', 'assistant', 'system', 'developer']))
      .optional(),
    min_depth: z.number().int().nonnegative().optional(),
    max_depth: z.number().int().nonnegative().optional(),
    missing_match: z.enum(['passthrough', 'empty']).optional(),
    trim_capture: z.boolean().optional(),
    trim_strings: z.array(z.string().max(16384)).max(100).optional(),
  })
  .passthrough()
export const editableRegexRulesSchema = z
  .object({
    mode: z.literal('rules'),
    failure_policy: z.enum(['passthrough', 'error']).optional(),
    enable_send: z.boolean().optional(),
    models: z.array(z.string()).optional(),
    rules: z.array(regexRuleSchema).max(100),
  })
  .passthrough()
export const regexRulesSchema = editableRegexRulesSchema.refine(
  (config) =>
    config.rules.every(
      (rule) =>
        (rule.disabled === true || rule.pattern.length > 0) &&
        (rule.stage !== 'send' ||
          rule.roles === undefined ||
          rule.roles.length > 0) &&
        (rule.min_depth === undefined ||
          rule.max_depth === undefined ||
          rule.min_depth <= rule.max_depth)
    ) &&
    new Set(config.rules.map((rule) => rule.id)).size === config.rules.length
)
export type RegexRule = z.infer<typeof regexRuleSchema>
export type RegexRulesConfig = z.infer<typeof regexRulesSchema>

export function importRegexScripts(value: unknown): {
  rules: RegexRule[]
  warnings: string[]
  originals: unknown[]
} {
  const scripts = Array.isArray(value) ? value : [value]
  const rules: RegexRule[] = []
  const warnings: string[] = []
  for (const item of scripts) {
    if (
      !item ||
      typeof item !== 'object' ||
      typeof item.findRegex !== 'string'
    ) {
      throw new Error('Invalid SillyTavern regex script')
    }
    const script = item as Record<string, unknown>
    const placements = Array.isArray(script.placement) ? script.placement : []
    const name =
      typeof script.scriptName === 'string' ? script.scriptName : 'Regex'
    const delimiter = item.findRegex.startsWith('/')
      ? item.findRegex.lastIndexOf('/')
      : -1
    const flags = delimiter > 0 ? item.findRegex.slice(delimiter + 1) : ''
    const unsupported =
      !/^[gimsu]*$/.test(flags) ||
      new Set(flags).size !== flags.length ||
      placements.some((p) => p !== 1 && p !== 2) ||
      !!script.substituteRegex ||
      (typeof script.replaceString === 'string' &&
        /\{\{(?!match\}\})/i.test(script.replaceString)) ||
      (Array.isArray(script.trimStrings) &&
        script.trimStrings.some(
          (trim) => typeof trim !== 'string' || trim.includes('{{')
        ))
    if (unsupported) warnings.push(name)
    const base = {
      name,
      pattern: item.findRegex,
      replacement:
        typeof script.replaceString === 'string' ? script.replaceString : '',
      action: 'replace' as const,
      disabled: script.disabled === true || unsupported,
      ...(Array.isArray(script.trimStrings) &&
      script.trimStrings.every((trim) => typeof trim === 'string')
        ? { trim_strings: script.trimStrings as string[] }
        : {}),
      ...(typeof script.minDepth === 'number' && script.minDepth >= 0
        ? { min_depth: script.minDepth }
        : {}),
      ...(typeof script.maxDepth === 'number' && script.maxDepth >= 0
        ? { max_depth: script.maxDepth }
        : {}),
    }
    const send = script.promptOnly === true || !script.markdownOnly
    const receive = script.markdownOnly === true || !script.promptOnly
    const roles: RegexRule['roles'] = []
    if (placements.includes(1)) roles.push('user')
    if (placements.includes(2)) roles.push('assistant')
    if (send && roles.length) {
      rules.push({ ...base, id: crypto.randomUUID(), stage: 'send', roles })
    }
    if (receive && placements.includes(2)) {
      rules.push({ ...base, id: crypto.randomUUID(), stage: 'receive' })
    }
    if (!roles.length && !unsupported) warnings.push(name)
  }
  return { rules, warnings, originals: scripts }
}
