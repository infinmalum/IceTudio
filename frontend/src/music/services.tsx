import { createContext, useContext } from "react";
import type { Op, TransformSpec } from "./projectTypes";
export type MusicServices = {
 GetMusicInputs:()=>Promise<{version:number;token:string;payload:string}|null>;
 SaveMusicInputs:(expectedToken:string,token:string,payload:string)=>Promise<void>;
 PrepareNoteTransform:(projectID:string,baseRevision:number,clipID:string,noteIDs:string[]|null,spec:TransformSpec)=>Promise<Op[]|null>;
 Audition:(request:{program:number;bank:number;keys:number[];velocity:number;durationMs:number})=>Promise<void>;
};
export const MusicServicesContext = createContext<MusicServices | null>(null);
export function useMusicServices() {
  const value = useContext(MusicServicesContext);
  if (!value) throw Error("Music services are not installed");
  return value;
}
