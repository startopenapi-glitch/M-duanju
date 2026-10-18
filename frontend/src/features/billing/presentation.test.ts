import { test, expect } from "vitest";
import { presentRecharge } from "./presentation";
import type { OrderProgress } from "./types";
const order = {
  id: "o",
  status: "paid",
  payMode: "wechat",
  amountCents: 10000,
  credits: 10000,
  credited: false,
  runId: "r",
  projectId: "p",
  paymentEvidence: "recorded",
  refund: null,
  observedAt: "",
} as OrderProgress;
test("paid_false_credit_is_not_success", () => {
  const p = presentRecharge(order, null);
  expect(p.ledger).toBe("pending");
  expect(p.continuation).toBe("unknown");
});
test("manual_approval_has_no_wechat_stage", () =>
  expect(
    presentRecharge(
      { ...order, payMode: "manual", paymentEvidence: "manual" },
      null,
    ).payment,
  ).toBe("manual"));
test("old_run_is_not_latest_run", () => {
  const c = {
    run: { id: "r", status: "completed" },
    project: { id: "p" },
    isLatest: false,
  } as any;
  expect(presentRecharge({ ...order, credited: true }, c).continuation).toBe(
    "superseded",
  );
});
test("refunded_credit_does_not_authorize_continuation", () =>
  expect(
    presentRecharge(
      {
        ...order,
        credited: true,
        refund: { status: "succeeded", amountCents: 10000 },
      },
      null,
    ).refund,
  ).toBe("succeeded"));
