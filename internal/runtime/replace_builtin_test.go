package runtime

import "testing"

func TestReplaceBuiltinOverridesWithoutPanic(t *testing.T) {
	reg := NewSlashRegistry()
	reg.ReplaceBuiltin(SlashCommand{Name: "acp-probe", Action: func(string) string { return "first" }})
	reg.ReplaceBuiltin(SlashCommand{Name: "acp-probe", Action: func(string) string { return "second" }})
	cmd, ok := reg.Lookup("acp-probe")
	if !ok {
		t.Fatal("replaced command missing")
	}
	if cmd.Action("") != "second" {
		t.Fatalf("action = %q, want second", cmd.Action(""))
	}
	if len(reg.Shadowed()) != 0 {
		t.Fatalf("same-tier replacement should not shadow: %+v", reg.Shadowed())
	}
}
