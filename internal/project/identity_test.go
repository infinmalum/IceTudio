package project

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestNewTransactionIdentityBoundary(t *testing.T) {
	st, err := Blank("边界", 480)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := ApplyTransaction(&st, []Op{{Kind: "track.add", Track: &TrackSpec{ID: "t_existing"}}}); err != nil {
		t.Fatal(err)
	}
	prior := map[string]bool{"t_retired": true}
	for _, id := range []string{"t_retired", "t_existing"} {
		before, _ := json.Marshal(st)
		forward, inverse, result, err := ApplyNewTransaction(&st, []Op{{Kind: "track.add", Track: &TrackSpec{ID: id}}}, prior)
		if err == nil || forward != nil || inverse != nil || !reflect.DeepEqual(result, Result{}) {
			t.Fatalf("reused ID returned partial outcome: %v %v %+v %v", forward, inverse, result, err)
		}
		after, _ := json.Marshal(st)
		if string(before) != string(after) {
			t.Fatal("rejection changed state")
		}
	}
	if !reflect.DeepEqual(prior, map[string]bool{"t_retired": true}) {
		t.Fatal("reservation input mutated")
	}
	ops := []Op{{Kind: "track.add", Track: &TrackSpec{ID: "t_fresh"}}}
	_, inverse, result, err := ApplyNewTransaction(&st, ops, prior)
	if err != nil || !reflect.DeepEqual(result.CreatedIDs, []string{"t_fresh"}) {
		t.Fatalf("new ID rejected: %+v %v", result, err)
	}
	if len(prior) != 1 {
		t.Fatal("success mutated caller reservation set")
	}
	// History execution can restore an identity that is already reserved.
	restore, _, err := ApplyTransaction(&st, inverse)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := ApplyTransaction(&st, restore); err != nil {
		t.Fatal(err)
	}
	if st.Tracks[1].ID != "t_fresh" {
		t.Fatal("history restoration changed identity")
	}
}
