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

import { Badge } from '@/components/ui/badge'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import type { Channel } from '@/features/channels/types'
import { formatQuota } from '@/lib/format'

interface RestrictedQuotaCellProps {
  restrictedQuota: number
  restrictedChannels: string // JSON array of channel IDs
  channelMap: Map<number, string> // id -> name mapping, fetched by parent
}

export function RestrictedQuotaCell({
  restrictedQuota,
  restrictedChannels,
  channelMap,
}: RestrictedQuotaCellProps) {
  const { t } = useTranslation()

  if (restrictedQuota <= 0) {
    return (
      <span className='text-muted-foreground text-sm'>-</span>
    )
  }

  let channelIds: number[] = []
  try {
    channelIds = JSON.parse(restrictedChannels || '[]') as number[]
  } catch {
    channelIds = []
  }

  const channelNames = channelIds
    .map((id) => channelMap.get(id) || `#${id}`)
    .join(', ')

  const displayChannels =
    channelNames.length > 30
      ? channelNames.slice(0, 30) + '...'
      : channelNames

  return (
    <div className='flex flex-col gap-1 min-w-[120px]'>
      <span className='font-medium tabular-nums text-sm'>
        {formatQuota(restrictedQuota)}
      </span>
      {channelIds.length > 0 && (
        <Tooltip>
          <TooltipTrigger
            render={
              <div className='flex flex-wrap gap-0.5 cursor-help' />
            }
          >
            {channelIds.slice(0, 3).map((id) => (
              <Badge
                key={id}
                variant='secondary'
                className='text-[10px] px-1 py-0 leading-tight'
              >
                {channelMap.get(id) || `#${id}`}
              </Badge>
            ))}
            {channelIds.length > 3 && (
              <Badge
                variant='outline'
                className='text-[10px] px-1 py-0 leading-tight'
              >
                +{channelIds.length - 3}
              </Badge>
            )}
          </TooltipTrigger>
          <TooltipContent>
            <div className='space-y-1 text-xs'>
              <div className='font-medium'>
                {t('Gifted Quota')}: {formatQuota(restrictedQuota)}
              </div>
              <div className='text-muted-foreground'>
                {t('Channels')}: {displayChannels}
              </div>
            </div>
          </TooltipContent>
        </Tooltip>
      )}
    </div>
  )
}
