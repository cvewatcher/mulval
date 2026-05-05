package config

import (
	"bytes"
	"encoding/json"

	"github.com/invopop/jsonschema"
	"github.com/pkg/errors"
	jsonschemaother "github.com/santhosh-tekuri/jsonschema/v5"
)

func (cfg *Config) Schema() ([]byte, error) {
	schema := cfg.JSONSchema()
	return json.MarshalIndent(schema, "", "  ")
}

func (cfg *Config) JSONSchema() *jsonschema.Schema {
	reflector := jsonschema.Reflector{}

	// Register the comments, but could fail once binary is compiled thus we drop the error
	_ = reflector.AddGoComments("github.com/cvewatcher/mulval", ".")

	// Reflect the config into a JSON schema, then set the version and ID
	ref := reflector.Reflect(cfg)
	ref.Version = "https://json-schema.org/draft/2020-12/schema"
	ref.ID = "" // No official ID for this microservice
	return ref
}

func Validate(cfg *Config) error {
	// Build schema loager
	schema, err := New().Schema()
	if err != nil {
		return errors.Wrap(err, "schema validation failed during schema generation")
	}

	// Transform into unstructured model
	b, _ := json.Marshal(cfg)
	var dec any
	_ = json.Unmarshal(b, &dec)

	// Generate schema validator and execute
	compiler := jsonschemaother.NewCompiler()
	if err := compiler.AddResource("schema.json", bytes.NewReader(schema)); err != nil {
		return err
	}
	sch, err := compiler.Compile("schema.json")
	if err != nil {
		return err
	}
	return sch.Validate(cfg)
}
