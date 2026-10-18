import type { CreationDraft } from "./types";

/**
 * 跨标签页共享“同一账号 + 同一创作草稿”的 requestKey。
 *
 * 目的：两个独立标签页同时提交同一草稿时，服务端仍只看到一个请求身份；
 * 而不同草稿、顺序创建的相同草稿不会被内容合并。
 *
 * 只保存 requestKey 和草稿哈希，不保存草稿正文；成功后立即释放；
 * 未确认的请求保留到用户清理或过期，避免刷新/丢响应后重复创建。
 */

const PREFIX = "drama_creation_gate:v1:";
const LOCK_PREFIX = "drama_creation_gate_lock:v1:";
const GATE_TTL_MS = 24 * 60 * 60 * 1000;

type Gate = {
  key: string;
  account: string;
  draftHash: string;
  at: number;
  /** 成功后的短冷却；0 表示请求尚未确认，保留到用户清理/24h。 */
  until?: number;
};

function storage(): Storage | null {
  try {
    return typeof localStorage === "undefined" ? null : localStorage;
  } catch {
    return null;
  }
}

function randomKey(): string {
  try {
    return crypto.randomUUID();
  } catch {
    return "k" + Date.now() + Math.random().toString(36).slice(2);
  }
}

function stableDraft(draft: CreationDraft) {
  return [
    (draft.idea || "").trim(),
    draft.aspect,
    draft.totalSeconds,
    (draft.filmReference || "").trim(),
    (draft.mood || "").trim(),
  ];
}

// FNV-1a：同步、稳定、无需 async crypto；只用于本地分桶，不承担安全职责。
function draftHash(draft: CreationDraft): string {
  const input = JSON.stringify(stableDraft(draft));
  const fnv = (offset: number) => {
    let h = offset;
    for (let i = 0; i < input.length; i++) {
      h ^= input.charCodeAt(i);
      h = Math.imul(h, 0x01000193) >>> 0;
    }
    return h.toString(16).padStart(8, "0");
  };
  // Two independent 32-bit lanes make an accidental cross-draft collision
  // negligible while remaining synchronous/local-only.
  return fnv(0x811c9dc5) + fnv(0x9e3779b9);
}

function gateKey(account: string, draft: CreationDraft): string {
  return PREFIX + account + ":" + draftHash(draft);
}

function readGateKey(key: string): Gate | null {
  const s = storage();
  if (!s) return null;
  try {
    const raw = s.getItem(key);
    if (!raw) return null;
    const gate = JSON.parse(raw) as Gate;
    if (
      !gate ||
      typeof gate.key !== "string" ||
      !gate.key ||
      typeof gate.at !== "number" ||
      Date.now() - gate.at > GATE_TTL_MS ||
      (typeof gate.until === "number" &&
        gate.until > 0 &&
        gate.until <= Date.now())
    ) {
      s.removeItem(key);
      return null;
    }
    return gate;
  } catch {
    return null;
  }
}

function writeGate(key: string, gate: Gate) {
  const s = storage();
  if (!s) return;
  try {
    s.setItem(key, JSON.stringify(gate));
  } catch {
    // 本地存储不可用时退回随机 key；服务端 requestKey 幂等仍然生效。
  }
}

function sleep(ms: number) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

async function withDecisionLock<T>(name: string, fn: () => Promise<T>): Promise<T> {
  const locks = (
    typeof navigator !== "undefined"
      ? (navigator as Navigator & {
          locks?: {
            request: <R>(
              name: string,
              options: { mode: "exclusive" },
              callback: () => Promise<R>,
            ) => Promise<R>;
          };
        }).locks
      : undefined
  );
  if (locks?.request) {
    try {
      return await locks.request(name, { mode: "exclusive" }, fn);
    } catch {
      // 浏览器不支持/被策略禁用时走 localStorage 后备锁。
    }
  }
  const s = storage();
  if (!s) return fn();
  const lockKey = LOCK_PREFIX + name;
  const owner = randomKey();
  const deadline = Date.now() + 2000;
  while (Date.now() < deadline) {
    try {
      const raw = s.getItem(lockKey);
      const held = raw ? (JSON.parse(raw) as { owner: string; expires: number }) : null;
      if (!held || held.expires <= Date.now()) {
        s.setItem(lockKey, JSON.stringify({ owner, expires: Date.now() + 2000 }));
        const check = JSON.parse(s.getItem(lockKey) || "null") as {
          owner?: string;
        } | null;
        if (check?.owner === owner) {
          try {
            return await fn();
          } finally {
            try {
              const current = JSON.parse(s.getItem(lockKey) || "null") as {
                owner?: string;
              } | null;
              if (current?.owner === owner) s.removeItem(lockKey);
            } catch {
              // ignore cleanup failure
            }
          }
        }
      }
    } catch {
      // ignore and retry
    }
    await sleep(10);
  }
  // 后备锁超时也不阻塞用户；极端的两个标签页同时创建由服务端 key 幂等兜底。
  return fn();
}

/** 返回该账号+草稿应复用的 requestKey；不存在时创建一个并登记。 */
export async function creationRequestKey(
  account: string,
  draft: CreationDraft,
): Promise<string> {
  if (!account) return randomKey();
  const key = gateKey(account, draft);
  const hash = draftHash(draft);
  return withDecisionLock(key, async () => {
    const existing = readGateKey(key);
    if (existing && existing.account === account && existing.draftHash === hash) {
      return existing.key;
    }
    const created = {
      key: randomKey(),
      account,
      draftHash: hash,
      at: Date.now(),
    };
    writeGate(key, created);
    return created.key;
  });
}

/**
 * 请求确认成功后保留一小段冷却，覆盖“两个标签页几乎同时提交”的窗口；
 * 同时允许冷启动后的同草稿新项目。只处理自己这个 key。
 */
export function releaseCreationGate(
  account: string,
  draft: CreationDraft,
  key: string,
) {
  const s = storage();
  if (!s || !account || !key) return;
  const slot = gateKey(account, draft);
  try {
    const current = readGateKey(slot);
    if (current?.key === key)
      s.setItem(
        slot,
        JSON.stringify({ ...current, until: Date.now() + 30_000 }),
      );
  } catch {
    // ignore
  }
}

/** 明确失败/用户清理时彻底移除，不保留冷却门。 */
export function clearCreationGate(
  account: string,
  draft: CreationDraft,
  key: string,
) {
  const s = storage();
  if (!s || !account || !key) return;
  const slot = gateKey(account, draft);
  try {
    const current = readGateKey(slot);
    if (current?.key === key) s.removeItem(slot);
  } catch {
    // ignore
  }
}
