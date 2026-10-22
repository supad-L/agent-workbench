package contractguard

import (
	"encoding/json"
	"testing"

	"agent-workbench/contracts"
)

func TestCanonicalHashDoesNotRoundLargeIntegers(t *testing.T) {
	a, err := CanonicalHash([]byte(`{"n":9007199254740992}`))
	if err != nil {
		t.Fatal(err)
	}
	b, err := CanonicalHash([]byte(`{"n":9007199254740993}`))
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatal("content hash lost integer precision")
	}
	if _, err := CanonicalHash([]byte(`{"a":1} {"b":2}`)); err == nil {
		t.Fatal("multiple JSON values accepted")
	}
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := contracts.Files.ReadFile("examples/" + name + ".json")
	if err != nil {
		t.Fatal(err)
	}
	return data
}
func change(t *testing.T, data []byte, mutate func(map[string]any)) []byte {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatal(err)
	}
	mutate(value)
	out, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestTaskAndReviewRemainWithinFixedPlan(t *testing.T) {
	validator, err := contracts.New()
	if err != nil {
		t.Fatal(err)
	}
	plan := fixture(t, "plan")
	hash, err := CanonicalHash(plan)
	if err != nil {
		t.Fatal(err)
	}
	task := change(t, fixture(t, "task"), func(v map[string]any) { v["plan_hash"] = hash })
	if err := TaskAgainstPlan(validator, plan, task); err != nil {
		t.Fatal(err)
	}
	unconfirmed := change(t, task, func(v map[string]any) { v["requirement_ids"] = []string{"REQ-NEW-HUMAN-GATE"} })
	if TaskAgainstPlan(validator, plan, unconfirmed) == nil {
		t.Fatal("invented requirement accepted")
	}
	review := fixture(t, "review")
	artifact := "sha256:" + repeatA(64)
	if err := ReviewAgainstTask(validator, task, review, artifact, "ATTEMPT-1", "coordinator", "worker"); err != nil {
		t.Fatal(err)
	}
	newBasis := change(t, review, func(v map[string]any) {
		v["findings"].([]any)[0].(map[string]any)["basis_id"] = "AC-NEW-HUMAN-APPROVAL"
	})
	if ReviewAgainstTask(validator, task, newBasis, artifact, "ATTEMPT-1", "coordinator", "worker") == nil {
		t.Fatal("new acceptance gate became a blocking finding")
	}
	if ReviewAgainstTask(validator, task, review, "sha256:"+repeatA(63)+"b", "ATTEMPT-1", "coordinator", "worker") == nil {
		t.Fatal("stale artifact review accepted")
	}
	if ReviewAgainstTask(validator, task, review, artifact, "ATTEMPT-1", "worker", "worker") == nil {
		t.Fatal("self-review accepted")
	}
}

func repeatA(n int) string {
	s := ""
	for range n {
		s += "a"
	}
	return s
}

func TestMessageIdentityAndRoleCannotBeSpoofed(t *testing.T) {
	validator, err := contracts.New()
	if err != nil {
		t.Fatal(err)
	}
	message := fixture(t, "message")
	current := Identity{ProjectID: "PROJECT-1", TaskID: "TASK-1", AttemptID: "ATTEMPT-1", PlanVersion: 1, ContractRevision: 1, ProjectRunEpoch: 1, TaskEpoch: 1, LeaseEpoch: 1}
	if err := AuthorizeMessage(validator, message, current, "worker-1", "worker"); err != nil {
		t.Fatal(err)
	}
	old := current
	old.LeaseEpoch = 2
	if AuthorizeMessage(validator, message, old, "worker-1", "worker") == nil {
		t.Fatal("old execution lease accepted")
	}
	if AuthorizeMessage(validator, message, current, "another-worker", "worker") == nil {
		t.Fatal("spoofed sender accepted")
	}
	if AuthorizeMessage(validator, message, current, "worker-1", "coordinator") == nil {
		t.Fatal("coordinator cannot impersonate worker question")
	}
	if AuthorizeMessage(validator, message, current, "worker-1", "user") == nil {
		t.Fatal("unknown principal role accepted")
	}
}

func TestConflictingPlanReferencesAndReviewAttempts(t *testing.T) {
	validator, err := contracts.New()
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"duplicate requirement ID", func(v map[string]any) { v["engineering_constraints"].([]any)[0].(map[string]any)["id"] = "REQ-1" }},
		{"unknown acceptance requirement", func(v map[string]any) {
			v["acceptance"].([]any)[0].(map[string]any)["requirement_ids"] = []string{"UNKNOWN"}
		}},
		{"duplicate acceptance ID", func(v map[string]any) { v["acceptance"].([]any)[0].(map[string]any)["id"] = "REQ-1" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan := change(t, fixture(t, "plan"), test.mutate)
			hash, err := CanonicalHash(plan)
			if err != nil {
				t.Fatal(err)
			}
			task := change(t, fixture(t, "task"), func(v map[string]any) { v["plan_hash"] = hash })
			if TaskAgainstPlan(validator, plan, task) == nil {
				t.Fatal("invalid plan linkage accepted")
			}
		})
	}
	task, review := fixture(t, "task"), fixture(t, "review")
	artifact := "sha256:" + repeatA(64)
	if ReviewAgainstTask(validator, task, review, artifact, "ATTEMPT-2", "coordinator", "worker") == nil {
		t.Fatal("old attempt accepted for a current review")
	}
	duplicate := change(t, review, func(v map[string]any) {
		findings := v["findings"].([]any)
		copyFinding := map[string]any{}
		for k, value := range findings[0].(map[string]any) {
			copyFinding[k] = value
		}
		copyFinding["problem"] = "same identifier but different problem"
		v["findings"] = append(findings, copyFinding)
	})
	if ReviewAgainstTask(validator, task, duplicate, artifact, "ATTEMPT-1", "coordinator", "worker") == nil {
		t.Fatal("duplicate finding identifier accepted")
	}
	message := change(t, fixture(t, "message"), func(v map[string]any) {
		v["kind"] = "task.cancel"
		v["payload"] = map[string]any{"reason": "worker tries to control execution"}
	})
	identity := Identity{ProjectID: "PROJECT-1", TaskID: "TASK-1", AttemptID: "ATTEMPT-1", PlanVersion: 1, ContractRevision: 1, ProjectRunEpoch: 1, TaskEpoch: 1, LeaseEpoch: 1}
	if AuthorizeMessage(validator, message, identity, "worker-1", "worker") == nil {
		t.Fatal("worker escalated to control operation")
	}
}
