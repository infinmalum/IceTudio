import type { NoteDiff, ObjectDiff } from "./projectTypes";
export interface ProjectEditPreview {
    "callId": string;
    "argumentsHash": string;
    "projectId": string;
    "title": string;
    "baseRevision": number;
    "startTick": number;
    "endTick": number;
    "diff": NoteDiff;
    "objects": ObjectDiff;
    "notesOnly": boolean;
    "originalEndTick": number;
    "candidateEndTick": number;
}
