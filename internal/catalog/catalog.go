// Package catalog is the declarative source of truth for public FreeAgent
// operations. Cobra commands, capability discovery, JSON schemas, and safety
// policy all consume this package.
package catalog

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

type Risk string

const (
	RiskRead      Risk = "read"
	RiskWrite     Risk = "write"
	RiskSend      Risk = "send"
	RiskDelete    Risk = "delete"
	RiskFinancial Risk = "financial"
	RiskControl   Risk = "control"
)

func (r Risk) Mutation() bool { return r != RiskRead }

type ValueType string

const (
	TypeString  ValueType = "string"
	TypeInteger ValueType = "integer"
	TypeBoolean ValueType = "boolean"
)

type Parameter struct {
	Name        string    `json:"name"`
	Flag        string    `json:"flag"`
	Description string    `json:"description"`
	Type        ValueType `json:"type"`
	Required    bool      `json:"required"`
}

func PathParameter(name, description string) Parameter {
	return Parameter{Name: name, Flag: strings.ReplaceAll(name, "_", "-"), Description: description, Type: TypeString, Required: true}
}

func QueryParameter(name, description string, valueType ValueType, required bool) Parameter {
	return Parameter{Name: name, Flag: strings.ReplaceAll(name, "_", "-"), Description: description, Type: valueType, Required: required}
}

type QueryConstraintKind string

const (
	QueryConstraintExactlyOne QueryConstraintKind = "exactly_one"
)

type QueryConstraint struct {
	Kind       QueryConstraintKind `json:"kind"`
	Parameters []string            `json:"parameters"`
}

func ExactlyOneQueryParameter(parameters ...string) QueryConstraint {
	return QueryConstraint{Kind: QueryConstraintExactlyOne, Parameters: append([]string(nil), parameters...)}
}

type AccessRequirement string

const (
	AccessConditional    AccessRequirement = "conditional"
	AccessAccountManager AccessRequirement = "account_manager"
	AccessNotDocumented  AccessRequirement = "not_documented"
)

type OutputKind string

const (
	OutputObject OutputKind = "object"
	OutputArray  OutputKind = "array"
	OutputString OutputKind = "string"
	OutputNull   OutputKind = "null"
	OutputRaw    OutputKind = "raw"
)

type Operation struct {
	ID                  string            `json:"id"`
	Noun                string            `json:"noun"`
	Verb                string            `json:"verb"`
	Summary             string            `json:"summary"`
	Method              string            `json:"method"`
	Path                string            `json:"path"`
	ResponseKey         string            `json:"response_key,omitempty"`
	OutputKind          OutputKind        `json:"output_kind"`
	Risk                Risk              `json:"risk"`
	RequiredAccessLevel int               `json:"required_access_level,omitempty"`
	AccessRequirement   AccessRequirement `json:"access_requirement,omitempty"`
	Paginated           bool              `json:"paginated"`
	MaxPerPage          int               `json:"max_per_page,omitempty"`
	BodyAllowed         bool              `json:"body_allowed"`
	BodyRequired        bool              `json:"body_required"`
	BodyRootKeys        []string          `json:"body_root_keys,omitempty"`
	PracticeOnly        bool              `json:"practice_only,omitempty"`
	PracticeSupported   bool              `json:"practice_supported"`
	PathParameters      []Parameter       `json:"path_parameters,omitempty"`
	QueryParameters     []Parameter       `json:"query_parameters,omitempty"`
	QueryConstraints    []QueryConstraint `json:"query_constraints,omitempty"`
	DocumentationURL    string            `json:"documentation_url"`
	MinimumAPIVersion   string            `json:"minimum_api_version,omitempty"`
	Deprecated          bool              `json:"deprecated,omitempty"`
	DeprecationMessage  string            `json:"deprecation_message,omitempty"`
}

func (o Operation) Mutation() bool { return o.Risk.Mutation() }

type Capability struct {
	ID                  string            `json:"id"`
	Noun                string            `json:"noun"`
	Verb                string            `json:"verb"`
	Command             string            `json:"command"`
	Summary             string            `json:"summary"`
	Method              string            `json:"method"`
	Path                string            `json:"path"`
	ResponseKey         string            `json:"response_key,omitempty"`
	OutputKind          OutputKind        `json:"output_kind"`
	Risk                Risk              `json:"risk"`
	RequiredAccessLevel int               `json:"required_access_level,omitempty"`
	AccessRequirement   AccessRequirement `json:"access_requirement,omitempty"`
	Paginated           bool              `json:"paginated"`
	MaxPerPage          int               `json:"max_per_page,omitempty"`
	BodyAllowed         bool              `json:"body_allowed"`
	BodyRequired        bool              `json:"body_required"`
	BodyRootKeys        []string          `json:"body_root_keys,omitempty"`
	PathParameters      []Parameter       `json:"path_parameters,omitempty"`
	QueryParameters     []Parameter       `json:"query_parameters,omitempty"`
	QueryConstraints    []QueryConstraint `json:"query_constraints,omitempty"`
	PracticeOnly        bool              `json:"practice_only,omitempty"`
	PracticeSupported   bool              `json:"practice_supported"`
	AllowedInReadOnly   bool              `json:"allowed_in_read_only"`
	DocumentationURL    string            `json:"documentation_url"`
	ConfirmationPolicy  string            `json:"confirmation_policy"`
	OutputModes         []string          `json:"output_modes"`
	MinimumAPIVersion   string            `json:"minimum_api_version,omitempty"`
	Deprecated          bool              `json:"deprecated,omitempty"`
	DeprecationMessage  string            `json:"deprecation_message,omitempty"`
	InputSchema         string            `json:"input_schema"`
	OutputSchema        string            `json:"output_schema"`
}

func (o Operation) Capability() Capability {
	confirmation := "none"
	if o.Mutation() {
		confirmation = "short_lived_token_required_for_every_mutation"
	}
	return Capability{
		ID:                  o.ID,
		Noun:                o.Noun,
		Verb:                o.Verb,
		Command:             "freeagent " + o.Noun + " " + o.Verb,
		Summary:             o.Summary,
		Method:              o.Method,
		Path:                o.Path,
		ResponseKey:         o.ResponseKey,
		OutputKind:          o.OutputKind,
		Risk:                o.Risk,
		RequiredAccessLevel: o.RequiredAccessLevel,
		AccessRequirement:   o.AccessRequirement,
		Paginated:           o.Paginated,
		MaxPerPage:          o.MaxPerPage,
		BodyAllowed:         o.BodyAllowed,
		BodyRequired:        o.BodyRequired,
		BodyRootKeys:        append([]string(nil), o.BodyRootKeys...),
		PathParameters:      append([]Parameter(nil), o.PathParameters...),
		QueryParameters:     append([]Parameter(nil), o.QueryParameters...),
		QueryConstraints:    cloneQueryConstraints(o.QueryConstraints),
		PracticeOnly:        o.PracticeOnly,
		PracticeSupported:   o.PracticeSupported,
		AllowedInReadOnly:   !o.Mutation(),
		DocumentationURL:    o.DocumentationURL,
		ConfirmationPolicy:  confirmation,
		OutputModes:         []string{"table", "json", "jsonl", "raw"},
		MinimumAPIVersion:   o.MinimumAPIVersion,
		Deprecated:          o.Deprecated,
		DeprecationMessage:  o.DeprecationMessage,
		InputSchema:         "freeagent://schema/" + o.ID + "/input",
		OutputSchema:        "freeagent://schema/" + o.ID + "/output",
	}
}

func cloneQueryConstraints(constraints []QueryConstraint) []QueryConstraint {
	cloned := make([]QueryConstraint, len(constraints))
	for index, constraint := range constraints {
		cloned[index] = constraint
		cloned[index].Parameters = append([]string(nil), constraint.Parameters...)
	}
	return cloned
}

type Catalog struct {
	operations []Operation
	byID       map[string]Operation
}

func New(operations []Operation) (*Catalog, error) {
	copied := append([]Operation(nil), operations...)
	sort.Slice(copied, func(i, j int) bool { return copied[i].ID < copied[j].ID })
	if err := Validate(copied); err != nil {
		return nil, err
	}
	byID := make(map[string]Operation, len(copied))
	for _, operation := range copied {
		byID[operation.ID] = operation
	}
	return &Catalog{operations: copied, byID: byID}, nil
}

func Default() *Catalog {
	catalog, err := New(defaultOperations())
	if err != nil {
		panic(fmt.Sprintf("invalid built-in FreeAgent catalog: %v", err))
	}
	return catalog
}

func (c *Catalog) Operations() []Operation {
	return append([]Operation(nil), c.operations...)
}

func (c *Catalog) Lookup(id string) (Operation, bool) {
	operation, ok := c.byID[id]
	return operation, ok
}

func (c *Catalog) Capabilities() []Capability {
	capabilities := make([]Capability, len(c.operations))
	for index, operation := range c.operations {
		capabilities[index] = operation.Capability()
	}
	return capabilities
}

var (
	idPattern          = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*\.[a-z0-9]+(?:-[a-z0-9]+)*$`)
	commandWordPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	parameterPattern   = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	placeholderPattern = regexp.MustCompile(`\{([a-z][a-z0-9_]*)\}`)
)

func Validate(operations []Operation) error {
	seenIDs := make(map[string]struct{}, len(operations))
	seenCommands := make(map[string]struct{}, len(operations))
	for index, operation := range operations {
		if !idPattern.MatchString(operation.ID) {
			return fmt.Errorf("operation %d has invalid ID %q", index, operation.ID)
		}
		if operation.ID != operation.Noun+"."+operation.Verb {
			return fmt.Errorf("operation %q ID must match noun and verb", operation.ID)
		}
		if !commandWordPattern.MatchString(operation.Noun) || !commandWordPattern.MatchString(operation.Verb) {
			return fmt.Errorf("operation %q has an invalid command word", operation.ID)
		}
		if _, exists := seenIDs[operation.ID]; exists {
			return fmt.Errorf("duplicate operation ID %q", operation.ID)
		}
		seenIDs[operation.ID] = struct{}{}
		command := operation.Noun + " " + operation.Verb
		if _, exists := seenCommands[command]; exists {
			return fmt.Errorf("duplicate command %q", command)
		}
		seenCommands[command] = struct{}{}
		if operation.Summary == "" {
			return fmt.Errorf("operation %q has no summary", operation.ID)
		}
		if !strings.HasPrefix(operation.DocumentationURL, "https://dev.freeagent.com/docs/") {
			return fmt.Errorf("operation %q has no official documentation URL", operation.ID)
		}
		if operation.MinimumAPIVersion != "" {
			if _, err := time.Parse(time.DateOnly, operation.MinimumAPIVersion); err != nil {
				return fmt.Errorf("operation %q has invalid minimum API version", operation.ID)
			}
		}
		if operation.Deprecated != (operation.DeprecationMessage != "") {
			return fmt.Errorf("operation %q deprecation flag and message must be set together", operation.ID)
		}
		switch operation.Method {
		case "GET", "POST", "PUT", "DELETE":
		default:
			return fmt.Errorf("operation %q has unsupported method %q", operation.ID, operation.Method)
		}
		if !strings.HasPrefix(operation.Path, "/v2/") || strings.Contains(operation.Path, ":") {
			return fmt.Errorf("operation %q must use a /v2/ path with {name} placeholders", operation.ID)
		}
		if operation.Method == "GET" && operation.Mutation() {
			return fmt.Errorf("operation %q marks GET as a mutation", operation.ID)
		}
		if operation.Method != "GET" && !operation.Mutation() {
			return fmt.Errorf("operation %q marks %s as read-only", operation.ID, operation.Method)
		}
		if operation.BodyRequired && !operation.BodyAllowed {
			return fmt.Errorf("operation %q requires a disallowed body", operation.ID)
		}
		if len(operation.BodyRootKeys) > 0 && !operation.BodyAllowed {
			return fmt.Errorf("operation %q declares roots for a disallowed body", operation.ID)
		}
		bodyRoots := make(map[string]struct{}, len(operation.BodyRootKeys))
		for _, root := range operation.BodyRootKeys {
			if !parameterPattern.MatchString(root) {
				return fmt.Errorf("operation %q has invalid body root key %q", operation.ID, root)
			}
			if _, exists := bodyRoots[root]; exists {
				return fmt.Errorf("operation %q repeats body root key %q", operation.ID, root)
			}
			bodyRoots[root] = struct{}{}
		}
		if operation.RequiredAccessLevel == 0 {
			switch operation.AccessRequirement {
			case AccessConditional, AccessAccountManager, AccessNotDocumented:
			default:
				return fmt.Errorf("operation %q has no access requirement", operation.ID)
			}
		} else {
			if operation.RequiredAccessLevel < 1 || operation.RequiredAccessLevel > 8 {
				return fmt.Errorf("operation %q has invalid access level", operation.ID)
			}
			if operation.AccessRequirement != "" {
				return fmt.Errorf("operation %q mixes numeric and non-numeric access requirements", operation.ID)
			}
		}
		switch operation.OutputKind {
		case OutputObject, OutputArray, OutputString, OutputNull, OutputRaw:
		default:
			return fmt.Errorf("operation %q has invalid output kind %q", operation.ID, operation.OutputKind)
		}
		if operation.Paginated && operation.OutputKind != OutputArray {
			return fmt.Errorf("operation %q is paginated but does not return an array", operation.ID)
		}
		if operation.MaxPerPage < 0 || operation.MaxPerPage > 0 && !operation.Paginated {
			return fmt.Errorf("operation %q has invalid maximum page size", operation.ID)
		}
		if operation.OutputKind == OutputRaw && operation.ResponseKey != "" {
			return fmt.Errorf("operation %q selects a response key from raw output", operation.ID)
		}
		if operation.ResponseKey != "" && !parameterPattern.MatchString(operation.ResponseKey) {
			return fmt.Errorf("operation %q has invalid response key %q", operation.ID, operation.ResponseKey)
		}
		params := make(map[string]Parameter, len(operation.PathParameters))
		for _, parameter := range operation.PathParameters {
			if !parameter.Required || parameter.Type != TypeString {
				return fmt.Errorf("operation %q path parameter %q must be a required string", operation.ID, parameter.Name)
			}
			if err := validateParameter(parameter); err != nil {
				return fmt.Errorf("operation %q has invalid path parameter: %w", operation.ID, err)
			}
			if _, exists := params[parameter.Name]; exists {
				return fmt.Errorf("operation %q repeats path parameter %q", operation.ID, parameter.Name)
			}
			params[parameter.Name] = parameter
		}
		matches := placeholderPattern.FindAllStringSubmatch(operation.Path, -1)
		if len(matches) != len(params) {
			return fmt.Errorf("operation %q path parameter declaration does not match its path", operation.ID)
		}
		for _, match := range matches {
			if _, exists := params[match[1]]; !exists {
				return fmt.Errorf("operation %q does not declare path parameter %q", operation.ID, match[1])
			}
		}
		queryParams := make(map[string]Parameter, len(operation.QueryParameters))
		for _, parameter := range operation.QueryParameters {
			if err := validateParameter(parameter); err != nil {
				return fmt.Errorf("operation %q has invalid query parameter: %w", operation.ID, err)
			}
			if _, exists := queryParams[parameter.Name]; exists {
				return fmt.Errorf("operation %q repeats query parameter %q", operation.ID, parameter.Name)
			}
			queryParams[parameter.Name] = parameter
		}
		for _, constraint := range operation.QueryConstraints {
			if constraint.Kind != QueryConstraintExactlyOne {
				return fmt.Errorf("operation %q has unsupported query constraint %q", operation.ID, constraint.Kind)
			}
			if len(constraint.Parameters) < 2 {
				return fmt.Errorf("operation %q exactly-one constraint needs at least two parameters", operation.ID)
			}
			seenParameters := make(map[string]struct{}, len(constraint.Parameters))
			for _, name := range constraint.Parameters {
				parameter, exists := queryParams[name]
				if !exists {
					return fmt.Errorf("operation %q query constraint references undeclared parameter %q", operation.ID, name)
				}
				if parameter.Required {
					return fmt.Errorf("operation %q exactly-one parameter %q must not also be required", operation.ID, name)
				}
				if _, exists := seenParameters[name]; exists {
					return fmt.Errorf("operation %q repeats query constraint parameter %q", operation.ID, name)
				}
				seenParameters[name] = struct{}{}
			}
		}
	}
	return nil
}

func validateParameter(parameter Parameter) error {
	if !parameterPattern.MatchString(parameter.Name) {
		return fmt.Errorf("invalid name %q", parameter.Name)
	}
	if parameter.Flag != strings.ReplaceAll(parameter.Name, "_", "-") {
		return fmt.Errorf("parameter %q has invalid flag %q", parameter.Name, parameter.Flag)
	}
	if parameter.Description == "" {
		return fmt.Errorf("parameter %q has no description", parameter.Name)
	}
	switch parameter.Type {
	case TypeString, TypeInteger, TypeBoolean:
		return nil
	default:
		return fmt.Errorf("parameter %q has unsupported type %q", parameter.Name, parameter.Type)
	}
}
