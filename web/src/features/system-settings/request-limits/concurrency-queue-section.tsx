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
import { Input } from '@/components/ui/input'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'

import {
  SettingsForm,
  SettingsSwitchContent,
  SettingsSwitchItem,
} from '../components/settings-form-layout'
import { SettingsPageFormActions } from '../components/settings-page-context'
import { SettingsSection } from '../components/settings-section'
import { useUpdateOption } from '../hooks/use-update-option'

const maxConcurrencyLimit = 2147483647

const isValidGroupLimitJSON = (value: string | undefined) => {
  if (!value || value.trim() === '') return true
  try {
    const parsed = JSON.parse(value)
    if (
      typeof parsed !== 'object' ||
      parsed === null ||
      Array.isArray(parsed)
    ) {
      return false
    }
    for (const val of Object.values(parsed)) {
      if (
        typeof val !== 'number' ||
        !Number.isInteger(val) ||
        val < 0 ||
        val > maxConcurrencyLimit
      ) {
        return false
      }
    }
    return true
  } catch {
    return false
  }
}

const createConcurrencyQueueSchema = (t: (key: string) => string) =>
  z.object({
    ConcurrencyQueueEnabled: z.boolean(),
    ConcurrencyQueueScope: z.enum(['user', 'token']),
    ConcurrencyQueueDefaultLimit: z.number().min(0).max(maxConcurrencyLimit),
    ConcurrencyQueueTimeoutSeconds: z.number().min(1).max(maxConcurrencyLimit),
    ConcurrencyQueueGroupLimit: z
      .string()
      .optional()
      .refine(isValidGroupLimitJSON, {
        message: t('Invalid JSON format or values out of allowed range'),
      }),
  })

type ConcurrencyQueueFormValues = z.infer<
  ReturnType<typeof createConcurrencyQueueSchema>
>

type ConcurrencyQueueSectionProps = {
  defaultValues: ConcurrencyQueueFormValues
}

export function ConcurrencyQueueSection({
  defaultValues,
}: ConcurrencyQueueSectionProps) {
  const { t } = useTranslation()
  const updateOption = useUpdateOption()

  const concurrencyQueueSchema = createConcurrencyQueueSchema(t)

  const form = useForm<ConcurrencyQueueFormValues>({
    resolver: zodResolver(concurrencyQueueSchema),
    mode: 'onChange', // Enable real-time validation
    defaultValues,
  })

  useEffect(() => {
    form.reset(defaultValues)
  }, [defaultValues, form])

  const onSubmit = async (values: ConcurrencyQueueFormValues) => {
    const updates = Object.entries(values).filter(
      ([key, value]) =>
        value !== defaultValues[key as keyof ConcurrencyQueueFormValues]
    )

    for (const [key, value] of updates) {
      await updateOption.mutateAsync({ key, value: value ?? '' })
    }
  }

  return (
    <SettingsSection title={t('Concurrency Queue')}>
      <Form {...form}>
        <SettingsForm onSubmit={form.handleSubmit(onSubmit)}>
          <SettingsPageFormActions
            onSave={form.handleSubmit(onSubmit)}
            isSaving={updateOption.isPending}
          />
          <FormField
            control={form.control}
            name='ConcurrencyQueueEnabled'
            render={({ field }) => (
              <SettingsSwitchItem>
                <SettingsSwitchContent>
                  <FormLabel>{t('Enable Concurrency Queue')}</FormLabel>
                  <FormDescription>
                    {t(
                      'Queue requests per client when concurrency exceeds the limit instead of rejecting them'
                    )}
                  </FormDescription>
                </SettingsSwitchContent>
                <FormControl>
                  <Switch
                    checked={field.value}
                    onCheckedChange={field.onChange}
                  />
                </FormControl>
              </SettingsSwitchItem>
            )}
          />

          <FormField
            control={form.control}
            name='ConcurrencyQueueScope'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Client Dimension')}</FormLabel>
                <Select onValueChange={field.onChange} value={field.value}>
                  <FormControl>
                    <SelectTrigger>
                      <SelectValue />
                    </SelectTrigger>
                  </FormControl>
                  <SelectContent>
                    <SelectItem value='user'>{t('Per User')}</SelectItem>
                    <SelectItem value='token'>{t('Per Token')}</SelectItem>
                  </SelectContent>
                </Select>
              </FormItem>
            )}
          />

          <div className='grid gap-4 md:grid-cols-2'>
            <FormField
              control={form.control}
              name='ConcurrencyQueueDefaultLimit'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Default Concurrency Limit')}</FormLabel>
                  <FormControl>
                    <Input
                      type='number'
                      min={0}
                      max={maxConcurrencyLimit}
                      step={1}
                      {...field}
                      onChange={(e) =>
                        field.onChange(Number.parseInt(e.target.value) || 0)
                      }
                    />
                  </FormControl>
                  <FormDescription>
                    {t('0 means unlimited. Group limits override this value.')}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />

            <FormField
              control={form.control}
              name='ConcurrencyQueueTimeoutSeconds'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Queue Timeout (seconds)')}</FormLabel>
                  <FormControl>
                    <Input
                      type='number'
                      min={1}
                      max={maxConcurrencyLimit}
                      step={1}
                      {...field}
                      onChange={(e) =>
                        field.onChange(Number.parseInt(e.target.value) || 1)
                      }
                    />
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />
          </div>

          <FormField
            control={form.control}
            name='ConcurrencyQueueGroupLimit'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Group Concurrency Limits')}</FormLabel>
                <FormControl>
                  <JsonCodeEditor
                    value={field.value || ''}
                    onChange={field.onChange}
                    name={field.name}
                    onBlur={field.onBlur}
                    textareaRef={field.ref}
                    placeholder='{"default": 2, "vip": 5}'
                    aria-invalid={Boolean(
                      form.formState.errors.ConcurrencyQueueGroupLimit
                    )}
                  />
                </FormControl>
                <FormDescription>
                  {t('JSON object, group name to limit')}
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
