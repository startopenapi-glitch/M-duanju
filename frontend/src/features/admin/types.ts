import type { GenerationRun, User } from '../../lib/api'
import type { Audit } from '../workspace/ui'
export type Order = {
  id: string
  userId: string
  amountYuan: string
  amountCents: number
  credits: number
  status: string
  channel: string
  credited: boolean
  mock?: boolean
  runId?: string
  createdAt: string
  paidAt?: string
  creditedAt?: string
  refundedAt?: string
  refunded_at?: string
  adminNote?: string
}
export type Refund = {
  id: string
  orderId: string
  refundNo: string
  status: string
  amountCents: number
  creditsRevoked: number
  creditShortfall: number
  reason: string
  error: string
  createdAt: string
  updatedAt: string
}
export type OrderDetail = {
  order: Order
  user: { id: string; email: string }
  run: GenerationRun | null
  refund: Refund | null
  events: Audit[]
}
export type Job = {
  id: string
  projectId: string
  projectTitle: string
  userId: string
  userEmail: string
  runId: string
  kind: string
  status: string
  phase: string
  upstreamId: string
  errorCode: string
  lastError: string
  reservedCredits: number
  attempts: number
  maxAttempts: number
  updatedAt: string
}
export type Credit = {
  id: string
  userId: string
  userEmail?: string
  amount: number
  balanceAfter: number
  kind: string
  note: string
  operatorId: string
  createdAt: string
}
export type UserDetail = {
  user: User
  version: number
  availableCredits: number
  reservedCredits: number
  projectsCount: number
  ordersCount: number
  transactions: Credit[]
}
export type Partner = {
  id: string
  userId: string
  name: string
  intro: string
  wechat: string
  code: string
  status: string
  version: number
  createdAt?: string
}

export type PartnerInvitation = {
  id: string
  email: string
  name: string
  intro: string
  wechat: string
  status: 'pending' | 'accepted' | 'revoked' | 'expired'
  invitedBy: string
  acceptedUserId?: string
  acceptedPartnerId?: string
  expiresAt: string
  sendCount: number
  lastSentAt: string
  acceptedAt?: string
  createdAt: string
}
