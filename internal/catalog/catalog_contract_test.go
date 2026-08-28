package catalog

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

func TestDocumentedResponseContracts(t *testing.T) {
	t.Parallel()
	tests := []struct {
		id          string
		responseKey string
		outputKind  OutputKind
		paginated   bool
	}{
		{"categories.list", "", OutputObject, false},
		{"categories.get", "", OutputObject, false},
		{"categories.create", "", OutputObject, false},
		{"categories.update", "", OutputObject, false},
		{"properties.get", "property", OutputObject, false},
		{"properties.create", "property", OutputObject, false},
		{"properties.update", "property", OutputObject, false},
		{"account-locks.get", "", OutputObject, false},
		{"account-locks.update", "", OutputNull, false},
		{"cis-bands.list", "available_bands", OutputArray, false},
		{"company.business-categories", "business_categories", OutputArray, false},
		{"company.tax-timeline", "timeline_items", OutputArray, false},
		{"journal-sets.opening-balances", "journal_set", OutputObject, false},
		{"sales-tax.rates", "sales_tax_rates", OutputArray, false},
		{"reports.profit-and-loss", "profit_and_loss_summary", OutputObject, false},
		{"reports.trial-balance", "trial_balance_summary", OutputArray, false},
		{"reports.trial-balance-opening-balances", "trial_balance_summary", OutputArray, false},
		{"invoices.timeline", "", OutputObject, false},
		{"payroll.get-year", "", OutputObject, false},
		{"payroll.get-period", "period", OutputObject, false},
		{"payroll-profiles.get", "profiles", OutputArray, false},
		{"practice.get", "", OutputObject, false},
		{"invoices.default-additional-text", "default_additional_text", OutputString, false},
		{"estimates.default-additional-text", "default_additional_text", OutputString, false},
		{"invoices.pdf", "pdf", OutputObject, false},
		{"estimates.pdf", "pdf", OutputObject, false},
		{"credit-notes.pdf", "pdf", OutputObject, false},
		{"corporation-tax-returns.mark-as-filed", "corporation_tax_return", OutputObject, false},
		{"vat-returns.mark-as-unpaid", "vat_return", OutputObject, false},
		{"self-assessment-returns.mark-as-paid", "self_assessment_return", OutputObject, false},
		{"final-accounts-reports.mark-as-filed", "final_accounts_report", OutputObject, false},
	}
	catalog := Default()
	for _, test := range tests {
		test := test
		t.Run(test.id, func(t *testing.T) {
			t.Parallel()
			operation := requireOperation(t, catalog, test.id)
			if operation.ResponseKey != test.responseKey || operation.OutputKind != test.outputKind || operation.Paginated != test.paginated {
				t.Fatalf("unexpected response contract: key=%q kind=%q paginated=%t", operation.ResponseKey, operation.OutputKind, operation.Paginated)
			}
		})
	}
}

func TestDocumentedAccessRequirements(t *testing.T) {
	t.Parallel()
	numeric := map[string]int{
		"capital-assets.list":                     6,
		"reports.cashflow":                        6,
		"contacts.list":                           1,
		"email-addresses.list":                    1,
		"tasks.list":                              1,
		"hire-purchases.list":                     5,
		"cis-bands.list":                          4,
		"cis-settings.get":                        8,
		"cis-settings.update":                     8,
		"users.create":                            1,
		"users.update-me":                         1,
		"final-accounts-reports.list":             7,
		"final-accounts-reports.get":              7,
		"corporation-tax-returns.mark-as-filed":   8,
		"corporation-tax-returns.mark-as-unfiled": 8,
		"corporation-tax-returns.mark-as-paid":    7,
	}
	catalog := Default()
	for id, want := range numeric {
		operation := requireOperation(t, catalog, id)
		if operation.RequiredAccessLevel != want || operation.AccessRequirement != "" {
			t.Errorf("%s access = %d/%q, want %d/numeric", id, operation.RequiredAccessLevel, operation.AccessRequirement, want)
		}
	}
	for id, want := range map[string]AccessRequirement{
		"attachments.get":       AccessConditional,
		"attachments.delete":    AccessConditional,
		"practice.get":          AccessAccountManager,
		"practice-clients.list": AccessAccountManager,
		"account-managers.me":   AccessAccountManager,
		"sales-tax.rates":       AccessNotDocumented,
	} {
		operation := requireOperation(t, catalog, id)
		if operation.RequiredAccessLevel != 0 || operation.AccessRequirement != want {
			t.Errorf("%s access = %d/%q, want 0/%q", id, operation.RequiredAccessLevel, operation.AccessRequirement, want)
		}
	}
}

func TestDocumentedInputAndPagingContracts(t *testing.T) {
	t.Parallel()
	catalog := Default()

	for _, id := range []string{"bank-transactions.list", "bank-transaction-explanations.list", "bank-transactions.import-statement"} {
		parameter := requireQueryParameter(t, requireOperation(t, catalog, id), "bank_account")
		if !parameter.Required || parameter.Type != TypeString {
			t.Errorf("%s bank_account is not a required string: %+v", id, parameter)
		}
	}

	for _, id := range []string{"notes.list", "notes.create"} {
		operation := requireOperation(t, catalog, id)
		requireQueryParameter(t, operation, "contact")
		requireQueryParameter(t, operation, "project")
		if len(operation.QueryConstraints) != 1 || operation.QueryConstraints[0].Kind != QueryConstraintExactlyOne || !slices.Equal(operation.QueryConstraints[0].Parameters, []string{"contact", "project"}) {
			t.Errorf("%s does not require exactly one parent: %+v", id, operation.QueryConstraints)
		}
	}

	task := requireOperation(t, catalog, "tasks.create")
	if !requireQueryParameter(t, task, "project").Required || !slices.Equal(task.BodyRootKeys, []string{"task"}) {
		t.Errorf("unexpected task create contract: %+v", task)
	}
	statement := requireOperation(t, catalog, "bank-transactions.import-statement")
	if !slices.Equal(statement.BodyRootKeys, []string{"statement"}) || !strings.Contains(statement.Summary, "JSON array") {
		t.Errorf("statement import is not explicitly JSON-only: %+v", statement)
	}
	for _, id := range []string{"reports.cashflow", "sales-tax.rates"} {
		operation := requireOperation(t, catalog, id)
		for _, parameter := range operation.QueryParameters {
			if parameter.Required {
				t.Errorf("%s incorrectly requires %s", id, parameter.Name)
			}
		}
	}

	clients := requireOperation(t, catalog, "practice-clients.list")
	minimalData := requireQueryParameter(t, clients, "minimal_data")
	if clients.MaxPerPage != 500 || minimalData.Type != TypeBoolean || minimalData.Required {
		t.Errorf("unexpected practice-client page contract: max=%d parameter=%+v", clients.MaxPerPage, minimalData)
	}

	directDebit := requireOperation(t, catalog, "invoices.create-direct-debit")
	if directDebit.BodyAllowed || directDebit.BodyRequired || directDebit.ResponseKey != "" || directDebit.OutputKind != OutputNull {
		t.Errorf("direct debit incorrectly declares request or response payloads: %+v", directDebit)
	}
	invoiceCreate := requireOperation(t, catalog, "invoices.create")
	if !slices.Equal(invoiceCreate.BodyRootKeys, []string{"invoice"}) {
		t.Errorf("invoice create does not expose its request root: %+v", invoiceCreate)
	}
	duplicate := requireOperation(t, catalog, "invoices.duplicate")
	if duplicate.BodyAllowed || duplicate.BodyRequired {
		t.Errorf("bodyless duplicate accepts input: %+v", duplicate)
	}
}

func TestDocumentedMissingOperationsAndBodyRoots(t *testing.T) {
	t.Parallel()
	catalog := Default()
	tests := []struct {
		id           string
		method       string
		path         string
		responseKey  string
		bodyRootKeys []string
	}{
		{"attachments.delete", "DELETE", "/v2/attachments/{id}", "", nil},
		{"estimate-items.create", "POST", "/v2/estimate_items", "estimate_item", []string{"estimate", "estimate_item"}},
		{"estimate-items.update", "PUT", "/v2/estimate_items/{id}", "", []string{"estimate_item"}},
		{"estimate-items.delete", "DELETE", "/v2/estimate_items/{id}", "", nil},
	}
	for _, test := range tests {
		operation := requireOperation(t, catalog, test.id)
		if operation.Method != test.method || operation.Path != test.path || operation.ResponseKey != test.responseKey || !slices.Equal(operation.BodyRootKeys, test.bodyRootKeys) {
			t.Errorf("%s contract mismatch: %+v", test.id, operation)
		}
	}
}

func TestDocumentedMutationRiskFamilies(t *testing.T) {
	t.Parallel()
	financial := map[string]struct{}{
		"bank-transaction-explanations": {}, "bills": {}, "credit-note-reconciliations": {},
		"credit-notes": {}, "estimate-items": {}, "estimates": {}, "expenses": {},
		"invoices": {}, "journal-sets": {}, "price-list-items": {}, "properties": {},
	}
	control := map[string]struct{}{
		"bank-accounts": {}, "capital-asset-types": {}, "categories": {}, "sales-tax-periods": {}, "users": {},
	}
	for _, operation := range Default().Operations() {
		if !operation.Mutation() || operation.Risk == RiskDelete || operation.Risk == RiskSend {
			continue
		}
		if _, ok := financial[operation.Noun]; ok && operation.Risk != RiskFinancial {
			t.Errorf("%s risk = %q, want %q", operation.ID, operation.Risk, RiskFinancial)
		}
		if _, ok := control[operation.Noun]; ok && operation.Risk != RiskControl {
			t.Errorf("%s risk = %q, want %q", operation.ID, operation.Risk, RiskControl)
		}
	}
}

func TestInputSchemasEnforceDocumentedQueryAndBodyContracts(t *testing.T) {
	t.Parallel()
	catalog := Default()

	notes := compileSchema(t, requireOperation(t, catalog, "notes.create"), SchemaInput)
	assertSchemaAccepts(t, notes, map[string]any{"query": map[string]any{"contact": "contact-url"}, "body": map[string]any{"note": map[string]any{"note": "text"}}})
	assertSchemaRejects(t, notes, map[string]any{"body": map[string]any{"note": map[string]any{}}})
	assertSchemaRejects(t, notes, map[string]any{"query": map[string]any{"contact": "contact-url", "project": "project-url"}, "body": map[string]any{"note": map[string]any{}}})

	statement := compileSchema(t, requireOperation(t, catalog, "bank-transactions.import-statement"), SchemaInput)
	assertSchemaAccepts(t, statement, map[string]any{"query": map[string]any{"bank_account": "bank-url"}, "body": map[string]any{"statement": []any{map[string]any{"dated_on": "2026-08-28"}}}})
	assertSchemaRejects(t, statement, map[string]any{"query": map[string]any{"bank_account": "bank-url"}, "body": map[string]any{}})

	estimateItem := compileSchema(t, requireOperation(t, catalog, "estimate-items.create"), SchemaInput)
	assertSchemaAccepts(t, estimateItem, map[string]any{"body": map[string]any{"estimate": "estimate-url", "estimate_item": map[string]any{}}})
	assertSchemaRejects(t, estimateItem, map[string]any{"body": map[string]any{"estimate_item": map[string]any{}}})
}

func TestMutationSchemaIdentifiesInputFileProperty(t *testing.T) {
	t.Parallel()
	schema, err := Schema(requireOperation(t, Default(), "invoices.create"), SchemaInput)
	if err != nil {
		t.Fatal(err)
	}
	if schema.CLIInputFileProperty != "body" {
		t.Fatalf("input file property = %q, want body", schema.CLIInputFileProperty)
	}
	if !strings.Contains(schema.Description, "--input accepts only the value of the body property") ||
		!strings.Contains(schema.Properties["body"].Description, "do not wrap it") {
		t.Fatalf("schema does not explain CLI mapping: %#v", schema)
	}
}

func TestBodylessSchemaDoesNotDescribeInputFile(t *testing.T) {
	t.Parallel()
	schema, err := Schema(requireOperation(t, Default(), "invoices.list"), SchemaInput)
	if err != nil {
		t.Fatal(err)
	}
	if schema.CLIInputFileProperty != "" || strings.Contains(schema.Description, "--input") {
		t.Fatalf("bodyless schema advertises an input file: %#v", schema)
	}
	if _, exists := schema.Properties["body"]; exists {
		t.Fatalf("bodyless schema contains a body property: %#v", schema)
	}
}

func TestOutputSchemasUseOutputKindNotPagination(t *testing.T) {
	t.Parallel()
	catalog := Default()

	categories := compileSchema(t, requireOperation(t, catalog, "categories.list"), SchemaOutput)
	assertSchemaAccepts(t, categories, map[string]any{
		"admin_expenses_categories": []any{map[string]any{"nominal_code": "001"}},
		"cost_of_sales_categories":  []any{},
		"income_categories":         []any{},
		"general_categories":        []any{},
	})
	assertSchemaRejects(t, categories, []any{map[string]any{"nominal_code": "001"}})

	additionalText := compileSchema(t, requireOperation(t, catalog, "invoices.default-additional-text"), SchemaOutput)
	assertSchemaAccepts(t, additionalText, "Pay within 7 days")
	assertSchemaRejects(t, additionalText, map[string]any{"default_additional_text": "Pay within 7 days"})

	pdf := compileSchema(t, requireOperation(t, catalog, "invoices.pdf"), SchemaOutput)
	assertSchemaAccepts(t, pdf, map[string]any{"content": "base64-data"})
	assertSchemaRejects(t, pdf, "base64-data")

	directDebit := compileSchema(t, requireOperation(t, catalog, "invoices.create-direct-debit"), SchemaOutput)
	assertSchemaAccepts(t, directDebit, nil)
	assertSchemaRejects(t, directDebit, map[string]any{})
}

func TestValidationRejectsInconsistentContractMetadata(t *testing.T) {
	t.Parallel()
	valid := Operation{
		ID: "things.list", Noun: "things", Verb: "list", Summary: "List things",
		Method: "GET", Path: "/v2/things", OutputKind: OutputObject, Risk: RiskRead,
		RequiredAccessLevel: 1, DocumentationURL: "https://dev.freeagent.com/docs/things",
	}
	tests := []struct {
		name   string
		mutate func(*Operation)
	}{
		{"paginated object", func(operation *Operation) { operation.Paginated = true }},
		{"page limit without pagination", func(operation *Operation) { operation.MaxPerPage = 100 }},
		{"missing access", func(operation *Operation) { operation.RequiredAccessLevel = 0 }},
		{"mixed access", func(operation *Operation) { operation.AccessRequirement = AccessConditional }},
		{"raw response key", func(operation *Operation) { operation.OutputKind, operation.ResponseKey = OutputRaw, "things" }},
		{"body roots without body", func(operation *Operation) { operation.BodyRootKeys = []string{"thing"} }},
		{"undeclared constrained query", func(operation *Operation) {
			operation.QueryConstraints = []QueryConstraint{ExactlyOneQueryParameter("one", "two")}
		}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			operation := valid
			test.mutate(&operation)
			if err := Validate([]Operation{operation}); err == nil {
				t.Fatal("invalid contract metadata was accepted")
			}
		})
	}
}

func TestCapabilityExposesExecutionContractMetadata(t *testing.T) {
	t.Parallel()
	operation := requireOperation(t, Default(), "practice-clients.list")
	capability := operation.Capability()
	if capability.OutputKind != OutputArray || capability.MaxPerPage != 500 || capability.AccessRequirement != AccessAccountManager {
		t.Fatalf("capability lost output, page, or access metadata: %+v", capability)
	}
	if len(capability.QueryParameters) != 1 || capability.QueryParameters[0].Name != "minimal_data" {
		t.Fatalf("capability lost query metadata: %+v", capability.QueryParameters)
	}
}

func requireOperation(t *testing.T, catalog *Catalog, id string) Operation {
	t.Helper()
	operation, exists := catalog.Lookup(id)
	if !exists {
		t.Fatalf("catalog is missing %s", id)
	}
	return operation
}

func requireQueryParameter(t *testing.T, operation Operation, name string) Parameter {
	t.Helper()
	for _, parameter := range operation.QueryParameters {
		if parameter.Name == name {
			return parameter
		}
	}
	t.Fatalf("%s is missing query parameter %s", operation.ID, name)
	return Parameter{}
}

func compileSchema(t *testing.T, operation Operation, direction SchemaDirection) *jsonschema.Schema {
	t.Helper()
	document, err := Schema(operation, direction)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	if err := compiler.AddResource(document.ID, value); err != nil {
		t.Fatal(err)
	}
	compiled, err := compiler.Compile(document.ID)
	if err != nil {
		t.Fatal(err)
	}
	return compiled
}

func assertSchemaAccepts(t *testing.T, schema *jsonschema.Schema, value any) {
	t.Helper()
	if err := schema.Validate(value); err != nil {
		t.Fatalf("schema rejected valid value %#v: %v", value, err)
	}
}

func assertSchemaRejects(t *testing.T, schema *jsonschema.Schema, value any) {
	t.Helper()
	if err := schema.Validate(value); err == nil {
		t.Fatalf("schema accepted invalid value %#v", value)
	}
}
