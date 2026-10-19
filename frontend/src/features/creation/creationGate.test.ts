import { beforeEach, expect, test } from "vitest";
import {
  clearCreationGate,
  creationRequestKey,
  releaseCreationGate,
} from "./creationGate";

const draft = {
  idea: "同一个创作草稿",
  aspect: "16:9" as const,
  totalSeconds: 15,
  filmReference: "film",
  mood: "mood",
};

beforeEach(() => localStorage.clear());

test("same account and draft share one request key across concurrent decisions", async () => {
  const [a, b] = await Promise.all([
    creationRequestKey("u", draft),
    creationRequestKey("u", draft),
  ]);
  expect(a).toBe(b);
  expect(await creationRequestKey("u", draft)).toBe(a);
});

test("different drafts and different accounts do not share a key", async () => {
  const base = await creationRequestKey("u", draft);
  const otherDraft = await creationRequestKey("u", { ...draft, idea: "另一个故事" });
  const otherUser = await creationRequestKey("v", draft);
  expect(otherDraft).not.toBe(base);
  expect(otherUser).not.toBe(base);
});

test("successful release keeps a short cooldown, explicit clear allows a new project", async () => {
  const first = await creationRequestKey("u", draft);
  releaseCreationGate("u", draft, first);
  // The cooldown exists for the near-simultaneous second tab.
  expect(await creationRequestKey("u", draft)).toBe(first);
  clearCreationGate("u", draft, first);
  const second = await creationRequestKey("u", draft);
  expect(second).not.toBe(first);
});
