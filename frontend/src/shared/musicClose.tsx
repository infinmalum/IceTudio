import { createContext, useContext, useLayoutEffect, useRef } from "react";
export type Blocker = { kind: "busy" | "draft"; message: string; version?: string; preserved?: boolean };
export type Register = (read: () => Blocker | null) => () => void;
export const CloseContext = createContext<Register | null>(null);
export const CloseFrozenContext = createContext(false);
export const useCloseFrozen = () => useContext(CloseFrozenContext);
export function useCloseBlocker(read: () => Blocker | null) {
  const register = useContext(CloseContext);
  if (!register) throw Error("Music editing requires CloseGuardProvider");
  const current = useRef(read); current.current = read;
  useLayoutEffect(() => register(() => current.current()), [register]);
}
