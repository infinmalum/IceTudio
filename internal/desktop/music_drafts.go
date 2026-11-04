package desktop

import (
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"sync"

	"icetudio/internal/app"
	"icetudio/internal/project"
)

// Native and Piano share the existing CAS file. The bridge exposes only its
// partition; neither renderer can clear the other's entries, including on retry.
const nativeInputPrefix = "Native · "

type musicDraft struct {
	Key    string `json:"key"`
	Raw    string `json:"raw"`
	Label  string `json:"label"`
	Title  string `json:"title"`
	Target struct {
		ID       string `json:"id"`
		Revision int64  `json:"revision"`
	} `json:"target"`
	TrackID string   `json:"trackID,omitempty"`
	ClipID  string   `json:"clipID,omitempty"`
	NoteIDs []string `json:"noteIDs,omitempty"`
}
type musicDraftState struct {
	Version int          `json:"version"`
	Entries []musicDraft `json:"entries"`
}
type musicDraftBackend interface {
	GetMusicInputs() (*app.MusicInputsRecord, error)
	SaveMusicInputs(string, string, string) error
}
type musicDraftStore struct {
	mu      sync.Mutex
	backend musicDraftBackend
}

func decodeMusicDrafts(r *app.MusicInputsRecord) (musicDraftState, error) {
	s := musicDraftState{Version: 1, Entries: []musicDraft{}}
	if r != nil && r.Payload != "" {
		dec := json.NewDecoder(strings.NewReader(r.Payload))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&s); err != nil {
			return s, err
		}
		if dec.Decode(new(any)) != io.EOF {
			return s, errors.New("草稿包含尾随内容")
		}
	}
	if s.Version != 1 {
		return s, errors.New("音乐输入版本不受支持；原记录保留")
	}
	return s, nil
}
func nativeDraft(e musicDraft) bool {
	if strings.HasPrefix(e.Label, nativeInputPrefix) {
		return true
	}
	switch e.Label {
	case "音符力度", "新音符通道（1–16）", "乐器程序（1–128）", "片段起点（tick）", "片段长度（tick）", "播放与音频设置":
		return true
	}
	return false
}
func (d *musicDraftStore) read() (*app.MusicInputsRecord, musicDraftState, error) {
	r, err := d.backend.GetMusicInputs()
	if err != nil {
		return r, musicDraftState{}, err
	}
	s, err := decodeMusicDrafts(r)
	return r, s, err
}
func (d *musicDraftStore) pianoRead() (*app.MusicInputsRecord, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	r, s, err := d.read()
	if err != nil || r == nil {
		return r, err
	}
	out := musicDraftState{Version: 1, Entries: []musicDraft{}}
	for _, e := range s.Entries {
		if !nativeDraft(e) {
			out.Entries = append(out.Entries, e)
		}
	}
	b, err := json.Marshal(out)
	if err != nil {
		return nil, err
	}
	copy := *r
	copy.Payload = string(b)
	return &copy, nil
}
func (d *musicDraftStore) pianoSave(expected, token, payload string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	r, old, err := d.read()
	if err != nil {
		return err
	}
	next, err := decodeMusicDrafts(&app.MusicInputsRecord{Payload: payload})
	if err != nil {
		return err
	}
	for _, e := range next.Entries {
		if nativeDraft(e) {
			return errors.New("卷帘不能修改 Native 表单草稿")
		}
	}
	// Preserve the original CAS: a Native write while Piano saves is a conflict,
	// never implicit authorization to overwrite it. A later retry may reread.
	if r != nil && r.Token != expected && r.Token != token {
		return errors.New("音乐输入已变化，请重新读取后保存")
	}
	for _, e := range old.Entries {
		if nativeDraft(e) {
			next.Entries = append(next.Entries, e)
		}
	}
	b, err := json.Marshal(next)
	if err != nil {
		return err
	}
	return d.backend.SaveMusicInputs(expected, token, string(b))
}
func (d *musicDraftStore) list() ([]musicDraft, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, s, err := d.read()
	return s.Entries, err
}

// put rereads and preserves the other partition, but never retries a failed CAS.
func (d *musicDraftStore) put(entry musicDraft, remove bool) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !nativeDraft(entry) {
		return errors.New("不是 Native 表单草稿")
	}
	r, s, err := d.read()
	if err != nil {
		return err
	}
	out := musicDraftState{Version: 1, Entries: []musicDraft{}}
	for _, e := range s.Entries {
		if e.Key != entry.Key {
			out.Entries = append(out.Entries, e)
		} else if remove && !reflect.DeepEqual(e, entry) {
			return errors.New("草稿已变化，未清除后来的输入")
		}
	}
	if !remove {
		out.Entries = append(out.Entries, entry)
	}
	b, err := json.Marshal(out)
	if err != nil {
		return err
	}
	expected := ""
	if r != nil {
		expected = r.Token
	}
	return d.backend.SaveMusicInputs(expected, project.NewID("draft"), string(b))
}
func newMusicDraft(target EditTarget, title, track, clip, label, raw string) musicDraft {
	label = nativeInputPrefix + label
	b, _ := json.Marshal([]string{target.ProjectID, track, clip, label})
	e := musicDraft{Key: string(b), Label: label, Title: title, TrackID: track, ClipID: clip, Raw: raw}
	e.Target.ID = target.ProjectID
	e.Target.Revision = target.Revision
	return e
}
