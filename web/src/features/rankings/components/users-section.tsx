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
import { Users } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { cn } from '@/lib/utils'

import { formatShare, formatTokens } from '../lib/format'
import type { UserRanking } from '../types'

type UsersSectionProps = {
  rows: UserRanking[]
}

const TOP_RANK_STYLES: Record<number, string> = {
  1: 'bg-amber-500/15 text-amber-600 dark:text-amber-400',
  2: 'bg-slate-400/15 text-slate-600 dark:text-slate-300',
  3: 'bg-orange-700/10 text-orange-700 dark:text-orange-400',
}

/**
 * User leaderboard: top users by token usage in the selected period.
 */
export function UsersSection(props: UsersSectionProps) {
  const { t } = useTranslation()

  return (
    <section className='bg-card overflow-hidden rounded-lg border'>
      <header className='border-b px-5 py-4'>
        <h2 className='text-foreground inline-flex items-center gap-2 text-base font-semibold'>
          <Users className='text-primary size-4' />
          {t('User leaderboard')}
        </h2>
        <p className='text-muted-foreground mt-1 text-sm'>
          {t('Top users by token usage in this period')}
        </p>
      </header>
      {props.rows.length === 0 ? (
        <div className='text-muted-foreground/80 px-5 py-8 text-center text-sm'>
          {t('No user activity in this period')}
        </div>
      ) : (
        <table className='w-full text-sm'>
          <thead>
            <tr className='text-muted-foreground/80 border-b text-left text-xs tracking-wider uppercase'>
              <th className='px-5 py-2 font-medium'>{t('Rank')}</th>
              <th className='px-3 py-2 font-medium'>{t('User')}</th>
              <th className='px-3 py-2 text-right font-medium'>{t('Tokens')}</th>
              <th className='px-5 py-2 text-right font-medium'>{t('Share')}</th>
            </tr>
          </thead>
          <tbody>
            {props.rows.map((row) => (
              <tr key={row.user_id} className='border-b last:border-b-0'>
                <td className='px-5 py-2.5'>
                  <span
                    className={cn(
                      'inline-flex h-6 w-6 items-center justify-center rounded-full font-mono text-xs font-semibold tabular-nums',
                      TOP_RANK_STYLES[row.rank] ??
                        'text-muted-foreground/80'
                    )}
                  >
                    {row.rank}
                  </span>
                </td>
                <td className='text-foreground max-w-0 truncate px-3 py-2.5 font-mono font-medium'>
                  {row.username}
                </td>
                <td className='text-foreground px-3 py-2.5 text-right font-mono font-semibold tabular-nums'>
                  {formatTokens(row.total_tokens)}
                </td>
                <td className='text-muted-foreground px-5 py-2.5 text-right font-mono tabular-nums'>
                  {formatShare(row.share)}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </section>
  )
}
