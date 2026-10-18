import { createContext, useContext, type ReactNode } from 'react'
import { useResource } from './resource'
import { useAuth } from '../../App'
import type { Partner } from '../admin/types'
export type PartnerIdentity =
  | Partner
  | { id: string; status: 'suspended' }
  | null
const Identity = createContext<ReturnType<
  typeof useResource<PartnerIdentity>
> | null>(null)
export function WorkspaceIdentity({ children }: { children: ReactNode }) {
  const { user } = useAuth(),
    resource = useResource<PartnerIdentity>(user ? '/partner/me' : null)
  return <Identity.Provider value={resource}>{children}</Identity.Provider>
}
export function usePartnerIdentity() {
  const shared = useContext(Identity)
  const local = useResource<PartnerIdentity>(shared ? null : '/partner/me')
  return shared || local
}
