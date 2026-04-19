package state

import "testing"

func TestApplyPatchAndReadOnlyIsolation(t *testing.T) {
	st, err := New("task-1", "trace-1", UserInput{Text: "hello"}, map[string]string{"env": "test"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	ro, err := st.ReadOnly()
	if err != nil {
		t.Fatalf("ReadOnly() error = %v", err)
	}
	snapshot := ro.Snapshot()
	snapshot.WorkingMemory["x"] = "should-not-leak"
	if _, ok := st.WorkingMemory["x"]; ok {
		t.Fatalf("readonly snapshot mutated original state")
	}
	patch := Patch{
		WorkingMemory: map[string]any{"intent": "general"},
		NodeOutputs: map[string]map[string]any{
			"intent_parse": {"intent": "general"},
		},
		Sensitive: map[string]string{"token": "secret"},
	}
	if _, err := st.ApplyPatch("intent_parse", patch, []string{"token"}); err != nil {
		t.Fatalf("ApplyPatch() error = %v", err)
	}
	if got := st.WorkingMemory["intent"]; got != "general" {
		t.Fatalf("intent = %v, want general", got)
	}
	if got := st.Sensitive["token"]; got != "secret" {
		t.Fatalf("sensitive token = %v, want secret", got)
	}
}
