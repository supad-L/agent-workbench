// Package contracts embeds the versioned platform schemas with network loading disabled.
package contracts

import (
	"bytes"
	"embed"
	"errors"
	"fmt"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// Files contains schemas and synthetic examples; it contains no credentials.
//
//go:embed *.schema.json examples/*.json
var Files embed.FS

// Names lists the v1 contracts required by the M0 interface baseline.
var Names = []string{"plan", "task", "connection", "review", "verification", "message", "account_resource", "runtime_event", "proxy_endpoint", "network_binding"}

// Validator contains immutable compiled schemas.
type Validator struct{ schemas map[string]*jsonschema.Schema }

type noExternalLoader struct{}

func (noExternalLoader) Load(string) (any, error) {
	return nil, errors.New("external schema loading disabled")
}

// New compiles all embedded contracts without making external network requests.
func New() (*Validator, error) {
	compiler := jsonschema.NewCompiler()
	compiler.UseLoader(noExternalLoader{})
	compiler.AssertFormat()
	for _, name := range Names {
		data, err := Files.ReadFile(name + ".schema.json")
		if err != nil {
			return nil, err
		}
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		if err := compiler.AddResource("https://agent-workbench.invalid/contracts/"+name+".schema.json", doc); err != nil {
			return nil, err
		}
	}
	validator := &Validator{schemas: map[string]*jsonschema.Schema{}}
	for _, name := range Names {
		schema, err := compiler.Compile("https://agent-workbench.invalid/contracts/" + name + ".schema.json")
		if err != nil {
			return nil, fmt.Errorf("compile contract %s: %w", name, err)
		}
		validator.schemas[name] = schema
	}
	return validator, nil
}

// Validate checks a bounded JSON document. Error messages never echo instance values.
func (v *Validator) Validate(name string, data []byte) error {
	schema, ok := v.schemas[name]
	if !ok {
		return errors.New("unknown contract")
	}
	if len(data) > 1<<20 {
		return errors.New("contract exceeds size limit")
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return errors.New("invalid contract JSON")
	}
	if err := schema.Validate(doc); err != nil {
		return fmt.Errorf("%s contract validation failed", name)
	}
	return nil
}
