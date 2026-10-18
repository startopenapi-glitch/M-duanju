import { useCallback, useEffect, useRef, useState } from "react";
import { useAuth } from "../../App";
import { api, errorMessage, useSessionScope, staleError } from "../../lib/api";

/** Each value belongs to one identity + URL. Late results never populate a different scope. */
export function useResource<T>(path: string | null) {
  const { user } = useAuth();
  const session = useSessionScope();
  const key = `${user?.id || ""}:${session}:${path || ""}`;
  const [attempt, setAttempt] = useState(0);
  const [state, setState] = useState<{ key: string; data?: T; error?: string }>(
    { key: "" },
  );
  useEffect(() => {
    if (!path) return;
    let alive = true;
    const controller = new AbortController();
    setState({ key });
    api
      .get<T>(path, controller.signal)
      .then((data) => {
        if (alive) setState({ key, data });
      })
      .catch((e) => {
        if (alive && !staleError(e)) setState({ key, error: errorMessage(e) });
      });
    return () => {
      alive = false;
      controller.abort();
    };
  }, [key, path, attempt]);
  const retry = useCallback(() => setAttempt((n) => n + 1), []);
  const current = state.key === key ? state : undefined;
  return {
    data: current?.data,
    error: current?.error || "",
    loading: !!path && !current?.error && current?.data === undefined,
    retry,
  };
}

/** Mutation feedback is private to the mounted account. It never clears a newer action. */
export function useAction() {
  const { user } = useAuth();
  const session = useSessionScope();
  const identity = `${user?.id}:${session}`;
  const current = useRef(identity);
  current.current = identity;
  const alive = useRef(true);
  const lock = useRef<string | null>(null);
  const [feedback, setFeedback] = useState({
    identity,
    busy: false,
    error: "",
    notice: "",
  });
  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
    };
  }, []);
  const run = async (
    action: () => Promise<unknown>,
    success: string,
    onSuccess?: (value: any) => void,
    onError?: (error: unknown) => void,
  ) => {
    if (lock.current === identity) return;
    const started = identity;
    lock.current = started;
    setFeedback({ identity: started, busy: true, error: "", notice: "" });
    try {
      const value = await action();
      if (alive.current && current.current === started) {
        setFeedback({
          identity: started,
          busy: false,
          error: "",
          notice: success,
        });
        onSuccess?.(value);
      }
    } catch (e) {
      if (alive.current && current.current === started && !staleError(e)) {
        setFeedback({
          identity: started,
          busy: false,
          error: errorMessage(e),
          notice: "",
        });
        onError?.(e);
      }
    } finally {
      if (alive.current && current.current === started) {
        lock.current = null;
        setFeedback((previous) =>
          previous.identity === started
            ? { ...previous, busy: false }
            : previous,
        );
      }
    }
  };
  return {
    run,
    ...(feedback.identity === identity
      ? feedback
      : { busy: false, error: "", notice: "" }),
  };
}
