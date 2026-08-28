package catalog

import (
	"bytes"
	"encoding/json"
	"sort"
	"strings"
	"testing"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

func TestDefaultCatalogIsBroadSortedAndValid(t *testing.T) {
	t.Parallel()
	catalog := Default()
	operations := catalog.Operations()
	if len(operations) < 120 {
		t.Fatalf("catalog is unexpectedly narrow: %d operations", len(operations))
	}
	ids := make([]string, len(operations))
	for index, operation := range operations {
		ids[index] = operation.ID
		if operation.DocumentationURL == "" {
			t.Fatalf("operation %s has no documentation URL", operation.ID)
		}
	}
	if !sort.StringsAreSorted(ids) {
		t.Fatal("catalog operations are not sorted")
	}
	for _, required := range []string{
		"account-locks.get", "account-locks.update", "practice-clients.list",
		"final-accounts-reports.get", "self-assessment-returns.mark-as-paid",
		"reports.trial-balance", "bank-transactions.import-statement",
	} {
		if _, ok := catalog.Lookup(required); !ok {
			t.Errorf("catalog is missing %s", required)
		}
	}
}

func TestNestedSchemasDoNotEmitEmptyRootMetadata(t *testing.T) {
	t.Parallel()
	operation, _ := Default().Lookup("invoices.create")
	for _, direction := range []SchemaDirection{SchemaInput, SchemaOutput} {
		schema, err := Schema(operation, direction)
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(schema)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(encoded), `"$schema":""`) || strings.Contains(string(encoded), `"$id":""`) || strings.Contains(string(encoded), `"title":""`) {
			t.Fatalf("schema contains invalid empty root metadata: %s", encoded)
		}
	}
}

func TestEveryGeneratedSchemaCompilesAsDraft2020(t *testing.T) {
	t.Parallel()
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	locations := make([]string, 0, len(Default().Operations())*2)
	for _, operation := range Default().Operations() {
		for _, direction := range []SchemaDirection{SchemaInput, SchemaOutput} {
			schema, err := Schema(operation, direction)
			if err != nil {
				t.Fatalf("generate %s %s schema: %v", operation.ID, direction, err)
			}
			encoded, err := json.Marshal(schema)
			if err != nil {
				t.Fatalf("encode %s %s schema: %v", operation.ID, direction, err)
			}
			var document any
			decoder := json.NewDecoder(bytes.NewReader(encoded))
			decoder.UseNumber()
			if err := decoder.Decode(&document); err != nil {
				t.Fatalf("decode %s %s schema: %v", operation.ID, direction, err)
			}
			if err := compiler.AddResource(schema.ID, document); err != nil {
				t.Fatalf("register %s %s schema: %v", operation.ID, direction, err)
			}
			locations = append(locations, schema.ID)
		}
	}
	for _, location := range locations {
		if _, err := compiler.Compile(location); err != nil {
			t.Errorf("compile %s as Draft 2020-12: %v", location, err)
		}
	}
}

func TestCatalogOmitsKnownDocumentationDefects(t *testing.T) {
	t.Parallel()
	catalog := Default()
	for _, omitted := range []string{"tasks.delete", "bank-transactions.delete", "price-list-items.delete", "payroll.mark-as-unpaid", "credit-notes.mark-as-cancelled", "depreciation-profiles.list"} {
		if _, exists := catalog.Lookup(omitted); exists {
			t.Errorf("unsafe undocumented operation was exposed: %s", omitted)
		}
	}
}

func TestGenericInputSchemaStatesThatPayloadIsIncomplete(t *testing.T) {
	t.Parallel()
	operation, ok := Default().Lookup("invoices.create")
	if !ok {
		t.Fatal("missing invoices.create")
	}
	schema, err := Schema(operation, SchemaInput)
	if err != nil {
		t.Fatal(err)
	}
	if schema.PayloadSchemaComplete == nil || *schema.PayloadSchemaComplete {
		t.Fatal("generic payload schema incorrectly claims completeness")
	}
	if schema.Properties["body"].AdditionalProperties != true {
		t.Fatal("generic body schema must permit documented FreeAgent fields")
	}
}

func TestCapabilityIncludesStableNounAndVerb(t *testing.T) {
	t.Parallel()
	operation, _ := Default().Lookup("invoices.create")
	capability := operation.Capability()
	if capability.Noun != "invoices" || capability.Verb != "create" {
		t.Fatalf("capability lost command grammar: %+v", capability)
	}
}

func TestValidationRejectsDuplicateAndGETMutation(t *testing.T) {
	t.Parallel()
	base := Operation{ID: "things.list", Noun: "things", Verb: "list", Summary: "List things", Method: "GET", Path: "/v2/things", Risk: RiskRead, DocumentationURL: "https://dev.freeagent.com/docs/things"}
	if err := Validate([]Operation{base, base}); err == nil {
		t.Fatal("duplicate operation was accepted")
	}
	base.Risk = RiskWrite
	if err := Validate([]Operation{base}); err == nil {
		t.Fatal("mutating GET was accepted")
	}
}
