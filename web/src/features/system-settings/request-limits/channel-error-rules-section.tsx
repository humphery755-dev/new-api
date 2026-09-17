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
import { zodResolver } from '@hookform/resolvers/zod'
import { useEffect } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import * as z from 'zod'

import { JsonCodeEditor } from '@/components/json-code-editor'
import {
  Form,
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'

import { SettingsForm } from '../components/settings-form-layout'
import { SettingsPageFormActions } from '../components/settings-page-context'
import { SettingsSection } from '../components/settings-section'
import { useUpdateOption } from '../hooks/use-update-option'

const channelErrorActions = new Set(['disable', 'retry_next', 'passthrough'])

const isValidRulesJSON = (value: string | undefined) => {
  if (!value || value.trim() === '') return true
  try {
    const parsed: unknown = JSON.parse(value)
    if (!Array.isArray(parsed)) return false
    for (const rule of parsed) {
      if (
        typeof rule !== 'object' ||
        rule === null ||
        !Array.isArray((rule as { keywords?: unknown }).keywords) ||
        (rule as { keywords: unknown[] }).keywords.length === 0 ||
        !channelErrorActions.has(
          (rule as { action?: unknown }).action as string
        )
      ) {
        return false
      }
    }
    return true
  } catch {
    return false
  }
}

const createChannelErrorRulesSchema = (t: (key: string) => string) =>
  z.object({
    ChannelErrorKeywordActions: z
      .string()
      .optional()
      .refine(isValidRulesJSON, {
        message: t('Invalid JSON format or values out of allowed range'),
      }),
  })

type ChannelErrorRulesFormValues = z.infer<
  ReturnType<typeof createChannelErrorRulesSchema>
>

type ChannelErrorRulesSectionProps = {
  defaultValues: ChannelErrorRulesFormValues
}

export function ChannelErrorRulesSection({
  defaultValues,
}: ChannelErrorRulesSectionProps) {
  const { t } = useTranslation()
  const updateOption = useUpdateOption()

  const channelErrorRulesSchema = createChannelErrorRulesSchema(t)

  const form = useForm<ChannelErrorRulesFormValues>({
    resolver: zodResolver(channelErrorRulesSchema),
    mode: 'onChange', // Enable real-time validation
    defaultValues,
  })

  useEffect(() => {
    form.reset(defaultValues)
  }, [defaultValues, form])

  const onSubmit = async (values: ChannelErrorRulesFormValues) => {
    const updates = Object.entries(values).filter(
      ([key, value]) =>
        value !== defaultValues[key as keyof ChannelErrorRulesFormValues]
    )

    for (const [key, value] of updates) {
      await updateOption.mutateAsync({ key, value: value ?? '[]' })
    }
  }

  return (
    <SettingsSection title={t('Channel Error Rules')}>
      <p className='text-muted-foreground text-sm'>
        {t(
          'Classify upstream errors by keywords and choose disable, retry-next, or passthrough'
        )}
      </p>
      <Form {...form}>
        <SettingsForm onSubmit={form.handleSubmit(onSubmit)}>
          <SettingsPageFormActions
            onSave={form.handleSubmit(onSubmit)}
            isSaving={updateOption.isPending}
          />
          <FormField
            control={form.control}
            name='ChannelErrorKeywordActions'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Channel Error Rules')}</FormLabel>
                <FormControl>
                  <JsonCodeEditor
                    value={field.value ?? ''}
                    onChange={field.onChange}
                    name={field.name}
                    onBlur={field.onBlur}
                    textareaRef={field.ref}
                    placeholder='[{"keywords":["余额不足"],"action":"disable"}]'
                    aria-invalid={Boolean(
                      form.formState.errors.ChannelErrorKeywordActions
                    )}
                  />
                </FormControl>
                <FormDescription>
                  {t(
                    'Ordered rules; first match wins. Actions: disable, retry_next, passthrough'
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />
        </SettingsForm>
      </Form>
    </SettingsSection>
  )
}
