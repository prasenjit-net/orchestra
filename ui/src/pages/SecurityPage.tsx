import { useEffect, useMemo, useState } from 'react'
import { FileClock, KeyRound, MonitorSmartphone, Users } from 'lucide-react'
import clsx from 'clsx'
import { useAuth } from '../auth/AuthProvider'
import SectionHeader from '../components/SectionHeader'
import APIKeysPanel from './security/APIKeysPanel'
import AuditPanel from './security/AuditPanel'
import SessionsPanel from './security/SessionsPanel'
import UserManagementPanel from './security/UserManagementPanel'

type TabID = 'users' | 'api-keys' | 'audit' | 'sessions'

export default function SecurityPage() {
  const { hasPermission } = useAuth()
  const tabs = useMemo(() => [
    { id: 'users' as const, label: 'Users', icon: Users, visible: hasPermission('user.read') },
    { id: 'api-keys' as const, label: 'API keys', icon: KeyRound, visible: hasPermission('api_key.read') },
    { id: 'audit' as const, label: 'Audit', icon: FileClock, visible: hasPermission('audit.read') },
    { id: 'sessions' as const, label: 'Sessions', icon: MonitorSmartphone, visible: hasPermission('session.manage_own') },
  ].filter((tab) => tab.visible), [hasPermission])
  const [active, setActive] = useState<TabID>((tabs[0]?.id ?? 'sessions') as TabID)

  useEffect(() => {
    if (!tabs.some((tab) => tab.id === active) && tabs[0]) setActive(tabs[0].id)
  }, [active, tabs])

  return (
    <div className="mx-auto max-w-7xl p-4 sm:p-6 lg:p-8">
      <SectionHeader title="Access control" description="Users, credentials, and security activity" />
      <div className="mt-6 border-b border-gray-200 dark:border-slate-800">
        <div className="flex gap-1 overflow-x-auto" role="tablist">
          {tabs.map((tab) => <button key={tab.id} type="button" role="tab" aria-selected={active === tab.id} onClick={() => setActive(tab.id)} className={clsx('flex shrink-0 items-center gap-2 border-b-2 px-4 py-3 text-sm font-medium', active === tab.id ? 'border-primary-600 text-primary-700 dark:text-primary-300' : 'border-transparent text-gray-500 hover:text-gray-900 dark:text-slate-400 dark:hover:text-slate-100')}><tab.icon className="h-4 w-4" />{tab.label}</button>)}
        </div>
      </div>
      <div className="mt-6">{active === 'users' && <UserManagementPanel />}{active === 'api-keys' && <APIKeysPanel />}{active === 'audit' && <AuditPanel />}{active === 'sessions' && <SessionsPanel />}</div>
    </div>
  )
}
