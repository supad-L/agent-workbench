// Package contractguard checks cross-document authority and stale-result boundaries.
package contractguard

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"agent-workbench/contracts"
	"agent-workbench/internal/workspace"
)

// Plan contains the immutable requirement and engineering-basis identifiers.
type Plan struct {
	ProjectID              string  `json:"project_id"`
	Version                int     `json:"version"`
	Requirements           []Basis `json:"requirements"`
	EngineeringConstraints []Basis `json:"engineering_constraints"`
	Acceptance             []struct {
		ID             string   `json:"id"`
		RequirementIDs []string `json:"requirement_ids"`
	} `json:"acceptance"`
}

// Basis identifies one confirmed requirement or engineering constraint.
type Basis struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

// Task is the subset of a task contract needed for authority checks.
type Task struct {
	ID           string          `json:"task_id"`
	ProjectID    string          `json:"project_id"`
	PlanVersion  int             `json:"plan_version"`
	PlanHash     string          `json:"plan_hash"`
	Revision     int             `json:"contract_revision"`
	Requirements []string        `json:"requirement_ids"`
	Bases        []string        `json:"basis_ids"`
	Scope        workspace.Scope `json:"scope"`
}

// CanonicalHash hashes a JSON value independently of object key order and whitespace.
func CanonicalHash(document []byte) (string, error) {
	if !json.Valid(document) {
		return "", errors.New("invalid JSON for content hash")
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return "", errors.New("invalid JSON for content hash")
	}
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

// TaskAgainstPlan checks references and scope. Human approval must be checked by the caller's store.
func TaskAgainstPlan(validator *contracts.Validator, planJSON, taskJSON []byte) error {
	if err := validator.Validate("plan", planJSON); err != nil {
		return err
	}
	if err := validator.Validate("task", taskJSON); err != nil {
		return err
	}
	var plan Plan
	var task Task
	if err := json.Unmarshal(planJSON, &plan); err != nil {
		return err
	}
	if err := json.Unmarshal(taskJSON, &task); err != nil {
		return err
	}
	hash, err := CanonicalHash(planJSON)
	if err != nil {
		return err
	}
	if plan.ProjectID != task.ProjectID || plan.Version != task.PlanVersion || hash != task.PlanHash {
		return errors.New("task does not match confirmed plan content")
	}
	bases := map[string]bool{}
	requirements := map[string]bool{}
	add := func(id string) error {
		if bases[id] {
			return errors.New("duplicate plan basis identifier")
		}
		bases[id] = true
		return nil
	}
	for _, basis := range plan.Requirements {
		if err := add(basis.ID); err != nil {
			return err
		}
		requirements[basis.ID] = true
	}
	for _, basis := range plan.EngineeringConstraints {
		if err := add(basis.ID); err != nil {
			return err
		}
	}
	for _, criterion := range plan.Acceptance {
		if err := add(criterion.ID); err != nil {
			return err
		}
		for _, id := range criterion.RequirementIDs {
			if !requirements[id] {
				return errors.New("acceptance references nonexistent requirement")
			}
		}
	}
	for _, id := range task.Requirements {
		if !requirements[id] {
			return errors.New("task introduces an unconfirmed requirement")
		}
	}
	for _, id := range task.Bases {
		if !bases[id] {
			return errors.New("task introduces an unconfirmed acceptance basis")
		}
	}
	return task.Scope.Validate()
}

// ReviewAgainstTask prevents stale artifacts, invented basis IDs and self-review.
// It validates structural authority, not the semantic truth of a review claim.
func ReviewAgainstTask(validator *contracts.Validator, taskJSON, reviewJSON []byte, artifactHash, currentAttemptID, reviewerID, implementerID string) error {
	if reviewerID == "" || reviewerID == implementerID {
		return errors.New("implementer cannot approve its own artifact")
	}
	if err := validator.Validate("task", taskJSON); err != nil {
		return err
	}
	if err := validator.Validate("review", reviewJSON); err != nil {
		return err
	}
	var task Task
	if err := json.Unmarshal(taskJSON, &task); err != nil {
		return err
	}
	var review struct {
		TaskID       string `json:"task_id"`
		AttemptID    string `json:"attempt_id"`
		PlanVersion  int    `json:"plan_version"`
		Revision     int    `json:"contract_revision"`
		ArtifactHash string `json:"artifact_hash"`
		Findings     []struct {
			ID      string `json:"id"`
			BasisID string `json:"basis_id"`
		} `json:"findings"`
	}
	if err := json.Unmarshal(reviewJSON, &review); err != nil {
		return err
	}
	if currentAttemptID == "" || review.AttemptID != currentAttemptID || task.ID != review.TaskID || task.PlanVersion != review.PlanVersion || task.Revision != review.Revision || review.ArtifactHash != artifactHash {
		return errors.New("review targets a stale or unrelated artifact")
	}
	allowed := map[string]bool{}
	seen := map[string]bool{}
	for _, id := range task.Bases {
		allowed[id] = true
	}
	for _, finding := range review.Findings {
		if !allowed[finding.BasisID] {
			return errors.New("review introduces a basis outside the task contract")
		}
		if seen[finding.ID] {
			return errors.New("duplicate review finding")
		}
		seen[finding.ID] = true
	}
	return nil
}

// Identity binds a message to one current task attempt and execution lease.
type Identity struct {
	ProjectID        string `json:"project_id"`
	TaskID           string `json:"task_id"`
	AttemptID        string `json:"attempt_id"`
	PlanVersion      int    `json:"plan_version"`
	ContractRevision int    `json:"contract_revision"`
	ProjectRunEpoch  int    `json:"project_run_epoch"`
	TaskEpoch        int    `json:"task_epoch"`
	LeaseEpoch       int    `json:"lease_epoch"`
}

// AuthorizeMessage uses the authenticated principal, never a sender role claimed by JSON.
func AuthorizeMessage(validator *contracts.Validator, document []byte, current Identity, principalID, principalRole string) error {
	if err := validator.Validate("message", document); err != nil {
		return err
	}
	var message struct {
		Identity
		SenderID string `json:"sender_id"`
		Kind     string `json:"kind"`
	}
	if err := json.Unmarshal(document, &message); err != nil {
		return err
	}
	if message.Identity != current {
		return errors.New("message belongs to a stale or unrelated execution")
	}
	if principalID == "" || principalID != message.SenderID {
		return errors.New("message sender does not match authenticated principal")
	}
	allowed := false
	switch principalRole {
	case "worker":
		allowed = message.Kind == "task.progress" || message.Kind == "task.submitted" || message.Kind == "task.question"
	case "coordinator":
		allowed = message.Kind == "task.dispatch" || message.Kind == "task.answer" || message.Kind == "task.review" || message.Kind == "task.cancel"
	}
	if !allowed {
		return fmt.Errorf("message kind is not permitted for this principal")
	}
	return nil
}
