import { useSyncExternalStore } from "react";
const TOKEN_KEY = "drama_token";
const AUTH_FREE = [
  "/auth/login",
  "/auth/register",
  "/auth/login-by-code",
  "/auth/email-code",
];
export const SESSION_EVENT = "drama:session";
let epoch = 0;
let heldToken = "";
let holds = 0;
let pendingExpiry = false;
const REVISION_KEY = "drama_session_revision";
export function getSessionScope() {
  let value = localStorage.getItem(REVISION_KEY);
  if (!value) {
    value = crypto.randomUUID();
    localStorage.setItem(REVISION_KEY, value);
  }
  return value;
}
function subscribeScope(listener: () => void) {
  const storage = (e: StorageEvent) => {
    if (e.key === TOKEN_KEY || e.key === REVISION_KEY || e.key === null)
      listener();
  };
  window.addEventListener(SESSION_EVENT, listener);
  window.addEventListener("storage", storage);
  return () => {
    window.removeEventListener(SESSION_EVENT, listener);
    window.removeEventListener("storage", storage);
  };
}
export function useSessionScope() {
  return useSyncExternalStore(subscribeScope, getSessionScope, getSessionScope);
}
function changed() {
  localStorage.setItem(REVISION_KEY, crypto.randomUUID());
  sessionStorage.removeItem("drama_creation_intent");
  Object.keys(sessionStorage).filter(k=>k.startsWith("drama_project_intent:")).forEach(k=>sessionStorage.removeItem(k));
  epoch++;
  if (typeof window !== "undefined")
    window.dispatchEvent(new Event(SESSION_EVENT));
}
export function getToken() {
  return localStorage.getItem(TOKEN_KEY) || "";
}
export function setToken(t: string) {
  localStorage.setItem(TOKEN_KEY, t);
  changed();
}
export function clearToken() {
  localStorage.removeItem(TOKEN_KEY);
  changed();
}
// A one-time recovery-code response must remain visible until acknowledged.
// This delays client navigation only; the server session remains revoked.
export function holdSessionExpiry() {
  const token = getToken();
  if (heldToken !== token) {
    heldToken = token;
    holds = 0;
    pendingExpiry = false;
  }
  holds++;
  let released = false;
  return () => {
    if (released) return;
    released = true;
    if (heldToken !== token) return;
    holds = Math.max(0, holds - 1);
    if (!holds && pendingExpiry && getToken() === token) {
      pendingExpiry = false;
      clearToken();
    }
  };
}
export class ApiError extends Error {
  status = 0;
  code = "";
  needTotp = false;
  requiredCredits = 0;
  availableCredits = 0;
  constructor(message: string, fields: Partial<ApiError> = {}) {
    super(message);
    Object.assign(this, fields);
  }
}
export const errorMessage = (e: unknown) =>
  e instanceof Error ? e.message : "操作失败，请重试";
export const staleError = (e: unknown) =>
  (e instanceof ApiError && e.code === "stale_response") ||
  (e instanceof DOMException && e.name === "AbortError");
async function req<T = any>(
  method: string,
  path: string,
  body?: unknown,
  signal?: AbortSignal,
): Promise<T> {
  const tk = getToken(),
    generation = epoch,
    session = getSessionScope();
  const headers: Record<string, string> = {
    "Content-Type": "application/json",
  };
  if (tk) headers.Authorization = `Bearer ${tk}`;
  const res = await fetch(`/api${path}`, {
    method,
    headers,
    body: body === undefined ? undefined : JSON.stringify(body),
    signal,
  });
  let data: Record<string, unknown>;
  try {
    data = await res.json();
  } catch {
    data = { ok: false, error: `HTTP ${res.status}` };
  }
  if (
    tk !== getToken() ||
    generation !== epoch ||
    session !== getSessionScope()
  )
    throw new ApiError("账号已切换，请重新加载", { code: "stale_response" });
  if (!res.ok || data.ok === false) {
    if (res.status === 401 && !AUTH_FREE.includes(path)) {
      if (holds > 0 && heldToken === tk) pendingExpiry = true;
      else clearToken();
    }
    throw new ApiError(
      typeof data.error === "string" ? data.error : `HTTP ${res.status}`,
      {
        status: res.status,
        code: typeof data.code === "string" ? data.code : "",
        needTotp: data.needTotp === true,
        requiredCredits:
          typeof data.requiredCredits === "number" ? data.requiredCredits : 0,
        availableCredits:
          typeof data.availableCredits === "number" ? data.availableCredits : 0,
      },
    );
  }
  return (data.data !== undefined ? data.data : data) as T;
}
export const api = {
  get: <T = any>(p: string, signal?: AbortSignal) =>
    req<T>("GET", p, undefined, signal),
  post: <T = any>(p: string, b?: unknown) => req<T>("POST", p, b ?? {}),
  patch: <T = any>(p: string, b?: unknown) => req<T>("PATCH", p, b ?? {}),
  del: <T = any>(p: string) => req<T>("DELETE", p),
};
export type Estimate = {
  estimatedCredits: number;
  suggestedBudgetYuan: number;
  resolution: string;
  segmentCount: number;
};
export type RunStatus =
  | "queued"
  | "running"
  | "waiting_credits"
  | "settlement_pending"
  | "completed"
  | "failed"
  | "cancelled";
export type GenerationRun = {
  id: string;
  projectId: string;
  projectVersion: number;
  mode: string;
  status: RunStatus;
  checkpoint: string;
  waitingReason?: string;
  autoResume: boolean;
  creditsEstimated: number;
  requiredCredits?: number;
  creditsSettled: number;
  requestedAt: string;
};
export type RunView = {
  outputs?: { runId: string; projectVersion: number; url: string }[];
  run: GenerationRun;
  availableCredits: number;
  shortfallCredits: number;
};
export type StartRunResult = {
  run: GenerationRun;
  availableCredits: number;
  estimatedCredits: number;
  shortfallCredits: number;
  waitingForCredits: boolean;
};
export type GenerationJob = {
  id: string;
  kind: string;
  status: string;
  phase: string;
  errorCode: string;
  lastError: string;
  canResume: boolean;
  manualRequired: boolean;
  attempts: number;
  maxAttempts: number;
  infrastructureAllowance: number;
  requiredCredits: number;
  availableCredits: number;
  shortfallCredits: number;
  reservedCredits: number;
};
export type User = {
  id: string;
  email: string;
  nickname: string;
  role: string;
  credits: number;
  status: string;
  createdAt: string;
};

export type Project = {
  id: string;
  title: string;
  idea: string;
  aspect: string;
  totalSeconds: number;
  status: string;
  budgetCredits: number;
  spentCredits: number;
  generationJobs?: GenerationJob[];
  reservedCredits?: number;
  keyvisualUrl: string;
  finalUrl: string;
  errorMsg: string;
  createdAt: string;
  updatedAt: string;
  characters: Character[];
  scenes: Scene[];
  shots: Shot[];
  reviews: Review[];
};
export type Character = {
  id: string;
  code: string;
  name: string;
  ageRange: string;
  appearance: string;
  wardrobe: string;
  persona: string;
  voiceProfile: string;
};
export type Scene = {
  id: string;
  code: string;
  name: string;
  location: string;
  timeOfDay: string;
  lighting: string;
  colorTemp: string;
  props: string[];
};
export type Shot = {
  id: string;
  shotCode: string;
  seq: number;
  duration: number;
  shotSize: string;
  cameraHeight: string;
  cameraMove: string;
  visualPrompt: string;
  speakerCode: string;
  dialogue: string;
  soundscape: string;
  musicCue: string;
  status: string;
  retryCount: number;
  videoUrl: string;
  durationActual: number;
  width: number;
  height: number;
  chargeYuan: number;
  frames: string[];
  retryLimit: number;
  errorMsg: string;
};
export type Review = {
  id: string;
  action: string;
  fromStatus: string;
  toStatus: string;
  note: string;
  createdAt: string;
};
