export type OrderProgress = {
  payment?: null | { qr?: string; h5Url?: string; expiresAt: string };
  id: string;
  status: string;
  payMode: string;
  amountCents: number;
  credits: number;
  credited: boolean;
  runId: string | null;
  projectId: string | null;
  paymentEvidence: "recorded" | "unknown" | "manual";
  refund: null | { status: string; amountCents: number };
  observedAt: string;
};
export type RechargeOrder = {
  id: string;
  amountCents: number;
  amountYuan: number | string;
  credits: number;
  status: string;
  payMode: string;
  createdAt: string;
  adminNote?: string;
};
export type CreditTransaction = {
  id: string;
  amount: number;
  balanceAfter: number;
  kind: string;
  note: string;
  createdAt: string;
};
