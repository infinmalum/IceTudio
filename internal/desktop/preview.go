package desktop

import (
	"sync"

	"icetudio/internal/app"
	"icetudio/internal/project"
)

// PianoContext is a versioned UI projection, never authoritative project state.
// Clip carries the complete selected clip; Notes is the bounded smoke sample.
type PianoContext struct {
	Sampled         bool                      `json:"-"`
	Pattern         []project.PatternInstance `json:"pattern,omitempty"`
	Candidate       *app.ProjectEditPreview   `json:"candidate,omitempty"`
	Snap            float64                   `json:"snap"`
	ReadOnly        bool                      `json:"readOnly"`
	ProtocolVersion int                       `json:"protocolVersion"`
	SessionID       string                    `json:"sessionId"`
	Writable        bool                      `json:"writable"`
	Track           project.Track             `json:"track"`
	Clip            project.Clip              `json:"clip"`
	Tempo           []project.TempoPoint      `json:"tempo"`
	Meter           []project.MeterPoint      `json:"meter"`
	Generation      uint64                    `json:"generation"`
	ProjectID       string                    `json:"projectId"`
	TrackID         string                    `json:"trackId"`
	ClipID          string                    `json:"clipId"`
	Revision        int64                     `json:"revision"`
	PPQ             int                       `json:"ppq"`
	Title           string                    `json:"title"`
	TotalNotes      int                       `json:"totalNotes"`
	Notes           []project.Note            `json:"notes"`
}

// PianoPreview is the isolated Piano Roll binding whitelist. Music writes use
// the shared Edits coordinator; storage, credentials and Agent are not exposed.
type PianoPreview struct {
	mu               sync.Mutex
	value            PianoContext
	session          string
	readyGeneration  uint64
	selectionVersion uint64
	selectionIDs     []string
	interactive      bool
	frozen           bool
	ackFault         bool // opt-in disposable native smoke only; never WebView configurable
	edits            *Edits
	service          *app.Service
	drafts           *musicDraftStore
	changed          func(string)
	invalidate       func()
	ready            func()
	navigation       func(PianoContext, string, int64, int64)
	notify           func()
}

func (p *PianoPreview) Context() PianoContext {
	p.mu.Lock()
	defer p.mu.Unlock()
	value := clonePianoContext(p.value)
	value.ReadOnly = p.edits == nil
	value.ProtocolVersion = 1
	value.SessionID = p.session
	if p.edits != nil {
		s := p.edits.State()
		value.Writable = !p.frozen && p.readyGeneration == p.value.Generation && s.Ready && !s.Busy && s.Pending == nil
	}
	return value
}

func (p *PianoPreview) set(value PianoContext) {
	p.mu.Lock()
	defer p.mu.Unlock()
	value.Generation = p.value.Generation + 1
	value = clonePianoContext(value)
	p.value = value
	p.selectionVersion = 0
	p.selectionIDs = nil
	p.interactive = false
}

func clonePianoContext(value PianoContext) PianoContext {
	value.Pattern = append([]project.PatternInstance(nil), value.Pattern...)
	value.Notes = append([]project.Note(nil), value.Notes...)
	value.Track.Clips = nil
	value.Clip.Notes = append([]project.Note(nil), value.Clip.Notes...)
	value.Clip.Controls = append([]project.ControlEvent(nil), value.Clip.Controls...)
	value.Clip.Raw = append([]project.RawEvent(nil), value.Clip.Raw...)
	for i := range value.Clip.Raw {
		value.Clip.Raw[i].Data = append([]byte(nil), value.Clip.Raw[i].Data...)
	}
	value.Tempo = append([]project.TempoPoint(nil), value.Tempo...)
	value.Meter = append([]project.MeterPoint(nil), value.Meter...)
	return value
}

func (p *PianoPreview) selectionCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.selectionIDs)
}
