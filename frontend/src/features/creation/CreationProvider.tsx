import { createContext, useContext, useState, type ReactNode } from "react";
import { useSessionScope } from "../../lib/api";
import { useCreateIntent } from "./useCreateIntent";
import type { CreationDraft } from "./types";
export const defaultDraft: CreationDraft = {
  idea: "",
  aspect: "16:9",
  totalSeconds: 15,
  filmReference: "教父式低调布光",
  mood: "克制、有张力",
};
function useCreation() {
  const intent = useCreateIntent(),
    [draft, setDraft] = useState<CreationDraft>(
      () => intent.draft || { ...defaultDraft },
    );
  return { draft, setDraft, intent };
}
const Ctx = createContext<ReturnType<typeof useCreation> | null>(null);
function SessionCreation({ children }: { children: ReactNode }) {
  const value = useCreation();
  return <Ctx.Provider value={value}>{children}</Ctx.Provider>;
}
export function useCreationSession() {
  const shared = useContext(Ctx);
  if (!shared) throw new Error("CreationProvider required");
  return shared;
}

export function CreationProvider({ children }: { children: ReactNode }) {
  const scope = useSessionScope();
  return <SessionCreation key={scope}>{children}</SessionCreation>;
}
