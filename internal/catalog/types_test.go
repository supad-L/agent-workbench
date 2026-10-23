package catalog

import "testing"

func TestTemplatesAreInactiveAndCannotSmuggleUnknownTools(t *testing.T) {
	for _, r := range Templates() {
		if r.Enabled || r.Primary.ConnectionID != "" {
			t.Fatal("template activated account")
		}
		if err := r.Validate(); err != nil {
			t.Fatal(r.Name, err)
		}
		r.Tools = append(r.Tools, "delete_user_data")
		if err := r.Validate(); err == nil {
			t.Fatal("unknown tool accepted")
		}
	}
	r := Templates()[0]
	r.Enabled = true
	if err := r.Validate(); err == nil {
		t.Fatal("enabled role without binding accepted")
	}
	r = Templates()[0]
	r.Fallback = Fallback{Enabled: true, Candidates: []Choice{{ConnectionID: "backup", ModelID: "model"}}, DataScope: "project_summary", Currency: "CNY", MaxExtraCost: 1000}
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
}
