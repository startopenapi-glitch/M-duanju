import { useEffect, type RefObject } from "react";
let locks = 0,
  previousOverflow = "";
export function useFocusScope(
  ref: RefObject<HTMLElement>,
  active: boolean,
  onEscape?: () => void,
) {
  useEffect(() => {
    if (!active || !ref.current) return;
    const root = ref.current,
      previous = document.activeElement as HTMLElement | null;
    if (locks++ === 0) {
      previousOverflow = document.body.style.overflow;
      document.body.style.overflow = "hidden";
    }
    const targets = () =>
      Array.from(
        root.querySelectorAll<HTMLElement>(
          'button:not(:disabled),a[href],input:not(:disabled),select:not(:disabled),textarea:not(:disabled),summary,[tabindex="0"]',
        ),
      ).filter((e) => {
        if (e.closest("[inert],[hidden]") || e.tabIndex < 0) return false;
        if (
          getComputedStyle(e).display === "none" ||
          getComputedStyle(e).visibility === "hidden"
        )
          return false;
        // Closed details descendants are not focusable; its own summary is.
        for (let a = e.parentElement; a && a !== root; a = a.parentElement) {
          if (
            a.tagName === "DETAILS" &&
            !(a as HTMLDetailsElement).open &&
            !a.querySelector(":scope > summary")?.contains(e)
          )
            return false;
          if (
            getComputedStyle(a).display === "none" ||
            getComputedStyle(a).visibility === "hidden"
          )
            return false;
        }
        return true;
      });
    const first = () => (targets()[0] || root).focus();
    first();
    const focus = (e: FocusEvent) => {
      if (!root.contains(e.target as Node)) first();
    };
    const key = (e: KeyboardEvent) => {
      if (e.key === "Escape" && onEscape) {
        e.preventDefault();
        onEscape();
      }
      if (e.key === "Tab") {
        const t = targets(),
          i = t.indexOf(document.activeElement as HTMLElement);
        if (!t.length) {
          e.preventDefault();
          root.focus();
        } else if (e.shiftKey && i <= 0) {
          e.preventDefault();
          t[t.length - 1].focus();
        } else if (!e.shiftKey && i === t.length - 1) {
          e.preventDefault();
          t[0].focus();
        }
      }
    };
    document.addEventListener("focusin", focus);
    document.addEventListener("keydown", key);
    return () => {
      document.removeEventListener("focusin", focus);
      document.removeEventListener("keydown", key);
      if (--locks === 0) document.body.style.overflow = previousOverflow;
      if (previous?.isConnected) previous.focus();
    };
  }, [ref, active, onEscape]);
}
