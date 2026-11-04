// Framework-independent project transport DTOs. Go project structs remain authoritative.
/**
 * Clip is a region on a track holding local events. Clips may overlap;
 * export orders by absolute tick with a stable tie-break.
 */
export interface Clip {
    "id": string;
    "name"?: string;

    /**
     * 调色板枚举，空 = 未设置（P1 §11）
     */
    "color"?: string;
    "start": number;
    "length": number;
    "notes": Note[] | null;
    "controls": ControlEvent[] | null;
    "raw": RawEvent[] | null;
}

export interface ClipPatch {
    "name"?: string | null;
    "color"?: string | null;
    "start"?: number | null;
    "length"?: number | null;
}

export interface ClipSpec {
    "id"?: string;
    "trackId"?: string;
    "name"?: string;

    /**
     * 调色板枚举（P1 §11）
     */
    "color"?: string;
    "start": number;
    "length": number;
}

/**
 * ControlEvent is a structured channel message (CC, bend, program,
 * aftertouch) at a local tick inside a clip.
 */
export interface ControlEvent {
    "id": string;
    "tick": number;
    "kind": string;
    "channel": number;
    "controller"?: number;
    "value"?: number;
    "program"?: number;
    "note"?: number;
}

export interface EventSpec {
    "id"?: string;
    "tick": number;
    "kind": string;
    "channel": number;
    "controller"?: number;
    "value"?: number;
    "program"?: number;
    "note"?: number;
}

/**
 * HistoryEntry is one committed transaction in the project's linear
 * history. Ops holds what was applied; Inverse holds how to undo it. For
 * undo entries Ops is the inverse that was applied; for redo entries Ops is
 * the target edit's ops re-applied.
 */
export interface HistoryEntry {
    "revision": number;
    "opId": string;
    "source": string;
    "baseRevision": number;

    /**
     * edit | undo | redo
     */
    "kind": string;
    "targetRevision"?: number;
    "ops"?: Op[] | null;
    "inverse"?: Op[] | null;
    "result": Result;
    "undone": boolean;
    "createdAt": string;
}

/**
 * MeterPoint is a time signature change.
 */
export interface MeterPoint {
    "tick": number;
    "numerator": number;
    "denominator": number;
}

export interface NewNote {
    "id"?: string;
    "start": number;
    "duration": number;
    "pitch": number;
    "velocity": number;
    "channel": number;
}

/**
 * Note is a pitched note with integer tick positions inside a clip. The
 * MIDI channel is a per-note property preserved on import; new notes
 * default to their track's channel (the submitter fills it in).
 */
export interface Note {
    "id": string;
    "start": number;
    "duration": number;
    "pitch": number;
    "velocity": number;
    "channel": number;
}

/**
 * NoteChange contains read-only before/after projections. IDs allocated in a
 * preview of note.add are temporary; only a committed Result owns stable IDs.
 */
export interface NoteChange {
    "trackId": string;
    "trackName": string;
    "clipId": string;
    "clipName": string;
    "clipStart": number;
    "before"?: Note | null;
    "after"?: Note | null;
}

export interface NoteDelta {
    "id": string;
    "start"?: number | null;
    "duration"?: number | null;
    "pitch"?: number | null;
    "velocity"?: number | null;
}

export interface NoteDiff {
    "added": number;
    "updated": number;
    "removed": number;
    "total": number;
    "changes": NoteChange[] | null;
}

/**
 * ObjectChange is a bounded, human-readable projection, never an edit payload.
 * Raw MIDI bytes are deliberately represented by counts, not transported here.
 */
export interface ObjectChange {
    "kind": string;
    "objectId": string;
    "trackName": string;
    "clipName": string;
    "field": string;
    "before": string;
    "after": string;
}

export interface ObjectDiff {
    "total": number;
    "changes": ObjectChange[] | null;
}

/**
 * Op is one edit instruction inside a transaction. Payloads are sparse:
 * only the fields relevant to Kind are read; update patches carry pointers
 * so "no change" is distinguishable from a zero value.
 */
export interface Op {
    "kind": string;

    /**
     * project.rename
     */
    "title"?: string;

    /**
     * tempo.set
     */
    "tempo"?: TempoPoint[] | null;

    /**
     * meter.set
     */
    "meter"?: MeterPoint[] | null;
    "trackId"?: string;

    /**
     * track.add
     */
    "track"?: TrackSpec | null;

    /**
     * track.update
     */
    "trackPatch"?: TrackPatch | null;
    "clipId"?: string;

    /**
     * clip.add
     */
    "clip"?: ClipSpec | null;

    /**
     * clip.update
     */
    "clipPatch"?: ClipPatch | null;

    /**
     * note.add
     */
    "notes"?: NewNote[] | null;

    /**
     * note.update
     */
    "noteDeltas"?: NoteDelta[] | null;

    /**
     * note.remove
     */
    "noteIds"?: string[] | null;

    /**
     * control.add
     */
    "events"?: EventSpec[] | null;

    /**
     * control.remove
     */
    "eventIds"?: string[] | null;

    /**
     * Inverse-only payloads, filled when applyOp builds the inverse of a
     * remove. ApplyNewTransaction rejects supplied positions (including zero)
     * and nonempty restoration slices; trusted history execution uses them
     * to restore nested objects and the original track order.
     */
    "invClips"?: Clip[] | null;
    "invNotes"?: Note[] | null;
    "invControls"?: ControlEvent[] | null;
    "invRaw"?: RawEvent[] | null;
    "invTrackIndex"?: number | null;
}

/**
 * RawEvent preserves bytes the model does not structure: SysEx, unmatched
 * note-offs, unknown or passthrough meta events. Export writes them back
 * verbatim.
 */
export interface RawEvent {
    "id": string;
    "tick": number;
    "data": string | null;
}

/**
 * Result is the durable outcome of one committed transaction. It is stored
 * with the history row so a caller that lost its response can query it by
 * opID instead of replaying the request.
 */
export interface Result {
    "opId": string;
    "revision": number;
    "baseRevision": number;
    "source": string;
    "applied": number;
    "summary": { [_ in string]?: number } | null;
    "createdIds"?: string[] | null;
}

/**
 * State is the serializable project content. The revision counter lives in
 * the storage row, not here.
 */
export interface State {
    "title": string;
    "ppq": number;
    "tempo": TempoPoint[] | null;
    "meter": MeterPoint[] | null;
    "tracks": Track[] | null;
}

/**
 * TempoPoint stores the exact SMF value (microseconds per quarter note).
 */
export interface TempoPoint {
    "tick": number;
    "usPerQuarter": number;
}

/**
 * Track owns clips. The MIDI channel is routing metadata, not identity.
 */
export interface Track {
    "id": string;
    "name"?: string;

    /**
     * 调色板枚举，空 = 未设置（P1 §11）
     */
    "color"?: string;
    "channel": number;
    "program": number;
    "muted"?: boolean;
    "solo"?: boolean;
    "clips": Clip[] | null;
}

export interface TrackPatch {
    "name"?: string | null;
    "color"?: string | null;
    "channel"?: number | null;
    "program"?: number | null;
    "muted"?: boolean | null;
    "solo"?: boolean | null;
}

export interface TrackSpec {
    "id"?: string;
    "name"?: string;

    /**
     * 调色板枚举（P1 §11）
     */
    "color"?: string;
    "channel": number;
    "program": number;
    "muted"?: boolean;
    "solo"?: boolean;
}

/**
 * TransformSpec is the agent-facing transform payload: exactly one of the
 * four variants. Values outside the documented ranges are rejected here so
 * the app layer can trust the compiled ops.
 */
export interface TransformSpec {
    "transpose"?: {"semitones": number} | null;
    "quantize"?: {"unit": string, "strength"?: number | null} | null;
    "velocity"?: {"scale"?: number | null, "offset"?: number | null, "set"?: number | null} | null;
    "shift"?: {"ticks": number} | null;
}
