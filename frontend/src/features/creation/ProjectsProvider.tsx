import {
  createContext,
  useContext,
  useEffect,
  useMemo,
  useState,
  useSyncExternalStore,
  type ReactNode,
} from "react";
import {
  api,
  errorMessage,
  staleError,
  useSessionScope,
  type Project,
} from "../../lib/api";
export const PROJECTS_EVENT = "drama:projects-changed";
export type ProjectFilter = { q?: string; status?: string };
type Snapshot = {
  items: Project[];
  total: number;
  loading: boolean;
  error: string;
};
class ProjectList {
  snapshot: Snapshot = { items: [], total: 0, loading: true, error: "" };
  listeners = new Set<() => void>();
  pages = 1;
  generation = 0;
  controller?: AbortController;
  pending = false;
  dirty = false;
  started = false;
  constructor(readonly query: string) {}
  subscribe = (f: () => void) => {
    this.listeners.add(f);
    return () => {
      this.listeners.delete(f);
    };
  };
  get = () => this.snapshot;
  publish(s: Partial<Snapshot>) {
    this.snapshot = { ...this.snapshot, ...s };
    this.listeners.forEach((f) => f());
  }
  async load() {
    if (this.pending) {
      this.dirty = true;
      return;
    }
    this.pending = true;
    this.started = true;
    const n = ++this.generation,
      c = new AbortController();
    this.controller = c;
    this.publish({ loading: true, error: "" });
    try {
      const results = await Promise.all(
        Array.from({ length: this.pages }, (_, i) =>
          api.get<{ items: Project[]; total: number }>(
            `/projects?page=${i + 1}&pageSize=20${this.query}`,
            c.signal,
          ),
        ),
      );
      if (n !== this.generation) return;
      this.publish({
        items: Array.from(
          new Map(
            results.flatMap((r) => r.items ?? []).map((p) => [p.id, p]),
          ).values(),
        ),
        total: results[0].total,
        loading: false,
      });
    } catch (e) {
      if (n === this.generation && !staleError(e))
        this.publish({ error: errorMessage(e), loading: false });
    } finally {
      if (n === this.generation) {
        this.pending = false;
        if (this.dirty) {
          this.dirty = false;
          void this.load();
        }
      }
    }
  }
  more = () => {
    if (this.pending || this.snapshot.items.length >= this.snapshot.total)
      return;
    this.pages++;
    void this.load();
  };
  retry = () => {
    void this.load();
  };
  dispose() {
    this.generation++;
    this.controller?.abort();
    this.pending = false;
    this.started = false;
    this.dirty = false;
  }
}
class ProjectCache {
  lists = new Map<string, ProjectList>();
  get(account: string, filter: ProjectFilter) {
    const q = filter.q?.trim() || "",
      status = filter.status || "",
      key = JSON.stringify([account, q, status]);
    let l = this.lists.get(key);
    if (!l) {
      const p = new URLSearchParams();
      if (q) p.set("q", q);
      if (status) p.set("status", status);
      l = new ProjectList(p.size ? "&" + p.toString() : "");
      this.lists.set(key, l);
    }
    return l;
  }
  refresh = () => this.refreshActive(false);
  refreshActive(passive: boolean) {
    this.lists.forEach((l) => {
      if (l.started && l.listeners.size && (!passive || !l.pending))
        void l.load();
    });
  }
  dispose = () => this.lists.forEach((l) => l.dispose());
}
function watchProjects(cache: ProjectCache) {
  const visible = () => {
    if (!document.hidden) cache.refreshActive(true);
  };
  window.addEventListener(PROJECTS_EVENT, cache.refresh);
  window.addEventListener("online", visible);
  document.addEventListener("visibilitychange", visible);
  return () => {
    window.removeEventListener(PROJECTS_EVENT, cache.refresh);
    window.removeEventListener("online", visible);
    document.removeEventListener("visibilitychange", visible);
    cache.dispose();
  };
}
const Ctx = createContext<ProjectCache | null>(null);
export function ProjectsProvider({ children }: { children: ReactNode }) {
  const scope = useSessionScope();
  const cache = useMemo(() => new ProjectCache(), [scope]);
  useEffect(() => watchProjects(cache), [cache]);
  return <Ctx.Provider value={cache}>{children}</Ctx.Provider>;
}
export function useSharedProjects(
  account: string,
  reloadKey = 0,
  filter: ProjectFilter = {},
) {
  const shared = useContext(Ctx),
    scope = useSessionScope(),
    local = useMemo(() => new ProjectCache(), [scope]),
    cache = shared || local;
  const list = cache.get(scope + ":" + account, filter);
  const data = useSyncExternalStore(list.subscribe, list.get, list.get);
  useEffect(() => {
    if (account && !list.pending) void list.load();
  }, [list, account]);
  useEffect(() => {
    if (account && reloadKey) list.retry();
  }, [list, reloadKey, account]);
  useEffect(() => {
    if (shared) return;
    return watchProjects(local);
  }, [shared, local]);
  return { ...data, retry: list.retry, more: list.more };
}
