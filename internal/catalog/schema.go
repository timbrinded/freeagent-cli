package catalog

import (
	"fmt"
	"strings"
)

type SchemaDirection string

const (
	SchemaInput  SchemaDirection = "input"
	SchemaOutput SchemaDirection = "output"
)

// JSONSchema is deliberately small and uses only standard JSON Schema
// vocabulary. It is serializable and stable; no reflection of Go SDK structs
// leaks into the agent contract.
type JSONSchema struct {
	Schema                string                `json:"$schema,omitempty"`
	ID                    string                `json:"$id,omitempty"`
	Title                 string                `json:"title,omitempty"`
	Description           string                `json:"description,omitempty"`
	Type                  string                `json:"type,omitempty"`
	Properties            map[string]JSONSchema `json:"properties,omitempty"`
	Items                 *JSONSchema           `json:"items,omitempty"`
	Required              []string              `json:"required,omitempty"`
	AllOf                 []JSONSchema          `json:"allOf,omitempty"`
	OneOf                 []JSONSchema          `json:"oneOf,omitempty"`
	AdditionalProperties  any                   `json:"additionalProperties,omitempty"`
	DocumentationURL      string                `json:"x-freeagent-documentation-url,omitempty"`
	PayloadSchemaComplete *bool                 `json:"x-freeagent-payload-schema-complete,omitempty"`
	CLIInputFileProperty  string                `json:"x-freeagent-cli-input-file-property,omitempty"`
}

func Schema(operation Operation, direction SchemaDirection) (JSONSchema, error) {
	switch direction {
	case SchemaInput:
		incomplete := false
		properties := make(map[string]JSONSchema)
		required := make([]string, 0, len(operation.PathParameters))
		for _, parameter := range operation.PathParameters {
			properties[parameter.Name] = JSONSchema{Type: string(parameter.Type), Description: parameter.Description}
			required = append(required, parameter.Name)
		}
		query := JSONSchema{
			Type:                 "object",
			Description:          "FreeAgent query parameters",
			Properties:           make(map[string]JSONSchema, len(operation.QueryParameters)),
			AdditionalProperties: JSONSchema{Type: "string"},
		}
		for _, parameter := range operation.QueryParameters {
			query.Properties[parameter.Name] = JSONSchema{Type: string(parameter.Type), Description: parameter.Description}
			if parameter.Required {
				query.Required = append(query.Required, parameter.Name)
			}
		}
		for _, constraint := range operation.QueryConstraints {
			branches := make([]JSONSchema, len(constraint.Parameters))
			for index, parameter := range constraint.Parameters {
				branches[index] = JSONSchema{Required: []string{parameter}}
			}
			query.AllOf = append(query.AllOf, JSONSchema{OneOf: branches})
		}
		if len(query.Required) > 0 || len(query.AllOf) > 0 {
			required = append(required, "query")
		}
		properties["query"] = query
		if operation.BodyAllowed {
			description := "Exact FreeAgent JSON request body supplied through --input; do not wrap it in another body property"
			if len(operation.BodyRootKeys) > 0 {
				description += "; required top-level keys: " + strings.Join(operation.BodyRootKeys, ", ")
			}
			properties["body"] = JSONSchema{
				Type:                 "object",
				Description:          description,
				Required:             append([]string(nil), operation.BodyRootKeys...),
				AdditionalProperties: true,
			}
			if operation.BodyRequired {
				required = append(required, "body")
			}
		}
		description := operation.Summary + ". Path properties map to command flags; query maps to query flags."
		if operation.BodyAllowed {
			description += " --input accepts only the value of the body property."
		}
		return JSONSchema{
			Schema:                "https://json-schema.org/draft/2020-12/schema",
			ID:                    "freeagent://schema/" + operation.ID + "/input",
			Title:                 operation.ID + " input",
			Description:           description,
			Type:                  "object",
			Properties:            properties,
			Required:              required,
			AdditionalProperties:  false,
			DocumentationURL:      operation.DocumentationURL,
			PayloadSchemaComplete: &incomplete,
			CLIInputFileProperty: func() string {
				if operation.BodyAllowed {
					return "body"
				}
				return ""
			}(),
		}, nil
	case SchemaOutput:
		incomplete := false
		resource := JSONSchema{Type: "object", AdditionalProperties: true}
		data := JSONSchema{}
		switch operation.OutputKind {
		case OutputObject:
			data = resource
		case OutputArray:
			data = JSONSchema{Type: "array", Items: &resource}
		case OutputString:
			data = JSONSchema{Type: "string"}
		case OutputNull:
			data = JSONSchema{Type: "null"}
		case OutputRaw:
			data = JSONSchema{Type: "string", Description: "The unparsed HTTP response body."}
		default:
			return JSONSchema{}, fmt.Errorf("operation %q has unknown output kind %q", operation.ID, operation.OutputKind)
		}
		return JSONSchema{
			Schema: "https://json-schema.org/draft/2020-12/schema",
			ID:     "freeagent://schema/" + operation.ID + "/output",
			Title:  operation.ID + " output data",
			Description: func() string {
				if data.Description != "" {
					return data.Description
				}
				return "The data field in the protocol version 1 success envelope."
			}(),
			Type:  data.Type,
			Items: data.Items,
			AdditionalProperties: func() any {
				if data.Type == "object" {
					return true
				}
				return nil
			}(),
			DocumentationURL:      operation.DocumentationURL,
			PayloadSchemaComplete: &incomplete,
		}, nil
	default:
		return JSONSchema{}, fmt.Errorf("unknown schema direction %q", direction)
	}
}
