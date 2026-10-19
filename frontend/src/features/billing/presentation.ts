import type { OrderProgress } from "./types";
import type { RunContext } from "../creation/types";

export function presentRecharge(
  order: OrderProgress,
  context: RunContext | null,
) {
  const refund = order.refund?.status || null;
  const payment =
    order.payMode === "manual"
      ? "manual"
      : order.paymentEvidence === "recorded"
        ? "recorded"
        : "unknown";
  const ledger = order.credited ? "credited" : "pending";
  let continuation = "unknown";
  if (
    context &&
    context.run.id === order.runId &&
    context.project.id === order.projectId
  ) {
    if (!context.isLatest) continuation = "superseded";
    else if (refund || context.hasUnresolvedSubmission)
      continuation = "checking";
    else if (order.credited) continuation = context.run.status;
  }
  return { payment, ledger, continuation, refund };
}
