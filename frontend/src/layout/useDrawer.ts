import { useCallback, useEffect, useRef, useState } from "react";
import { useFocusScope } from "./useFocusScope";
export function useDrawer() {
  const [open, setOpen] = useState(false),
    [mobile, setMobile] = useState(
      () => window.matchMedia?.("(max-width: 767px)").matches ?? false,
    );
  const ref = useRef<HTMLDivElement>(null);
  const closeDrawer = useCallback(() => setOpen(false), []);
  useEffect(() => {
    const mq = window.matchMedia?.("(max-width: 767px)");
    if (!mq) return;
    const change = () => {
      setMobile(mq.matches);
      if (!mq.matches) setOpen(false);
    };
    mq.addEventListener("change", change);
    return () => mq.removeEventListener("change", change);
  }, []);
  useEffect(() => {
    if (ref.current) ref.current.inert = mobile && !open;
  }, [mobile, open]);
  // Remove inert before the focus scope tries to focus the first control.
  useFocusScope(ref, open && mobile, closeDrawer);
  return { open, mobile, ref, openDrawer: () => setOpen(true), closeDrawer };
}
