package catalog

import "strings"

type resourceDeclaration struct {
	noun      string
	path      string
	key       string
	access    int
	verbs     string
	paginated bool
}

func defaultOperations() []Operation {
	var operations []Operation

	// These declarations include only resource verbs documented by FreeAgent.
	// They do not infer complete CRUD support from a list endpoint.
	resources := []resourceDeclaration{
		{"bank-accounts", "/v2/bank_accounts", "bank_accounts", 6, "LCUDE", true},
		{"bank-transaction-explanations", "/v2/bank_transaction_explanations", "bank_transaction_explanations", 6, "LCUDE", true},
		{"bills", "/v2/bills", "bills", 5, "LCUDE", true},
		{"capital-asset-types", "/v2/capital_asset_types", "capital_asset_types", 8, "LCUDE", true},
		{"categories", "/v2/categories", "", 2, "LCUDE", false},
		{"contacts", "/v2/contacts", "contacts", 1, "LCUDE", true},
		{"credit-note-reconciliations", "/v2/credit_note_reconciliations", "credit_note_reconciliations", 4, "LCUDE", true},
		{"credit-notes", "/v2/credit_notes", "credit_notes", 4, "LCUDE", true},
		{"estimates", "/v2/estimates", "estimates", 4, "LCUDE", true},
		{"expenses", "/v2/expenses", "expenses", 2, "LCUDE", true},
		{"journal-sets", "/v2/journal_sets", "journal_sets", 7, "LCUDE", true},
		{"invoices", "/v2/invoices", "invoices", 4, "LCUDE", true},
		{"notes", "/v2/notes", "notes", 3, "LCUDE", true},
		{"projects", "/v2/projects", "projects", 3, "LCUDE", true},
		{"properties", "/v2/properties", "properties", 7, "LCUDE", true},
		{"timeslips", "/v2/timeslips", "timeslips", 1, "LCUDE", true},
		{"users", "/v2/users", "users", 7, "LCUDE", true},
		// FreeAgent currently documents no DELETE route for price list items
		// that is consistent with its plural collection route.
		{"price-list-items", "/v2/price_list_items", "price_list_items", 4, "LCU", true},
		// The documented task DELETE route points at /users/:id, so it is not
		// exposed until a sandbox contract test establishes the real route.
		{"tasks", "/v2/tasks", "tasks", 1, "LCU", true},
	}
	for _, resource := range resources {
		operations = append(operations, resourceOperations(resource)...)
	}

	readResources := []resourceDeclaration{
		{"attachments", "/v2/attachments", "attachments", 0, "CE", false},
		{"bank-feeds", "/v2/bank_feeds", "bank_feeds", 6, "LC", true},
		// FreeAgent's bank-transaction DELETE example uses a conflicting
		// singular path. Import is represented as an explicit operation below.
		{"bank-transactions", "/v2/bank_transactions", "bank_transactions", 6, "LC", true},
		{"capital-assets", "/v2/capital_assets", "capital_assets", 6, "LC", true},
		{"hire-purchases", "/v2/hire_purchases", "hire_purchases", 5, "LC", true},
		{"recurring-invoices", "/v2/recurring_invoices", "recurring_invoices", 4, "LC", true},
		{"stock-items", "/v2/stock_items", "stock_items", 4, "LC", true},
	}
	for _, resource := range readResources {
		operations = append(operations, resourceOperations(resource)...)
	}

	operations = append(operations,
		read("company.get", "company", "get", "Get company details", "/v2/company", "company", 1, false),
		withOutputKind(read("company.business-categories", "company", "business-categories", "List valid business categories", "/v2/company/business_categories", "business_categories", 1, false), OutputArray),
		withOutputKind(read("company.tax-timeline", "company", "tax-timeline", "Get the company tax timeline", "/v2/company/tax_timeline", "timeline_items", 7, false), OutputArray),
		read("email-addresses.list", "email-addresses", "list", "List email addresses", "/v2/email_addresses", "email_addresses", 1, true),
		withOutputKind(read("cis-bands.list", "cis-bands", "list", "List CIS deduction bands", "/v2/cis_bands", "available_bands", 4, false), OutputArray),
		read("cis-settings.get", "cis-settings", "get", "Get CIS settings", "/v2/cis_settings", "cis_settings", 8, false),
		mutation("cis-settings.update", "cis-settings", "update", "Update CIS settings", "PUT", "/v2/cis_settings", "cis_settings", RiskControl, 8, true),
		read("account-locks.get", "account-locks", "get", "Get the accounting lock dates", "/v2/account_locks", "", 8, false),
		withOutputKind(withBodyRootKeys(mutation("account-locks.update", "account-locks", "update", "Update the accounting lock dates", "PUT", "/v2/account_locks", "", RiskControl, 8, true), "account_locks"), OutputNull),
		withOutputKind(mutation("account-locks.delete", "account-locks", "delete", "Delete the accounting lock dates", "DELETE", "/v2/account_locks", "", RiskDelete, 8, false), OutputNull),
		read("expenses.mileage-settings", "expenses", "mileage-settings", "Get mileage settings", "/v2/expenses/mileage_settings", "mileage_settings", 2, false),
		read("journal-sets.opening-balances", "journal-sets", "opening-balances", "Get opening balance journal entries", "/v2/journal_sets/opening_balances", "journal_set", 7, false),
		withQueryParameters(
			withOutputKind(withAccessRequirement(read("sales-tax.rates", "sales-tax", "rates", "List EC MOSS sales tax rates", "/v2/ec_moss/sales_tax_rates", "sales_tax_rates", 0, false), AccessNotDocumented), OutputArray),
			QueryParameter("country", "ISO country code used to select the sales tax rates", TypeString, false),
			QueryParameter("date", "Date used to select the applicable sales tax rates", TypeString, false),
		),
		read("users.me", "users", "me", "Get the authenticated user", "/v2/users/me", "user", 1, false),
		mutation("users.update-me", "users", "update-me", "Update the authenticated user", "PUT", "/v2/users/me", "user", RiskControl, 1, true),
		read("sales-tax-periods.list", "sales-tax-periods", "list", "List sales tax periods", "/v2/sales_tax_periods", "sales_tax_periods", 4, true),
		read("sales-tax-periods.get", "sales-tax-periods", "get", "Get one sales tax period", "/v2/sales_tax_periods/{id}", "sales_tax_period", 4, false, PathParameter("id", "Exact sales tax period ID")),
		mutation("sales-tax-periods.create", "sales-tax-periods", "create", "Create one sales tax period", "POST", "/v2/sales_tax_periods", "sales_tax_period", RiskControl, 8, true),
		mutation("sales-tax-periods.update", "sales-tax-periods", "update", "Update one sales tax period", "PUT", "/v2/sales_tax_periods/{id}", "sales_tax_period", RiskControl, 8, true, PathParameter("id", "Exact sales tax period ID")),
		withOutputKind(mutation("sales-tax-periods.delete", "sales-tax-periods", "delete", "Delete one sales tax period", "DELETE", "/v2/sales_tax_periods/{id}", "", RiskDelete, 8, false, PathParameter("id", "Exact sales tax period ID")), OutputNull),
	)

	operations = append(operations, reportOperations()...)
	operations = append(operations, invoiceOperations()...)
	operations = append(operations, estimateOperations()...)
	operations = append(operations, estimateItemOperations()...)
	operations = append(operations, creditNoteOperations()...)
	operations = append(operations, taxOperations()...)
	operations = append(operations, payrollOperations()...)
	operations = append(operations, practiceOperations()...)

	operations = append(operations,
		withOutputKind(withBodyRootKeys(
			withQueryParameters(
				mutation("bank-transactions.import-statement", "bank-transactions", "import-statement", "Import a bank statement from a JSON array of transactions", "POST", "/v2/bank_transactions/statement", "", RiskFinancial, 6, true),
				QueryParameter("bank_account", "Bank account URL that will receive the imported transactions", TypeString, true),
			),
			"statement",
		), OutputNull),
		mutation("timeslips.start-timer", "timeslips", "start-timer", "Start a timeslip timer", "POST", "/v2/timeslips/{id}/timer", "timeslip", RiskWrite, 1, false, PathParameter("id", "Exact timeslip ID")),
		mutation("timeslips.stop-timer", "timeslips", "stop-timer", "Stop a timeslip timer", "DELETE", "/v2/timeslips/{id}/timer", "timeslip", RiskWrite, 1, false, PathParameter("id", "Exact timeslip ID")),
	)

	for index := range operations {
		applyDocumentedMetadata(&operations[index])
		operations[index].DocumentationURL = documentationURL(operations[index].Noun, operations[index].Verb)
	}
	return operations
}

func resourceOperations(resource resourceDeclaration) []Operation {
	var operations []Operation
	if strings.Contains(resource.verbs, "L") {
		operation := read(resource.noun+".list", resource.noun, "list", "List "+humanize(resource.noun), resource.path, resource.key, resource.access, resource.paginated)
		operations = append(operations, operation)
	}
	identifier := "id"
	if resource.noun == "categories" {
		identifier = "nominal_code"
	}
	parameter := PathParameter(identifier, "Exact "+humanize(identifier))
	itemPath := resource.path + "/{" + identifier + "}"
	singularKey := strings.TrimSuffix(resource.key, "s")
	switch resource.noun {
	case "categories":
		singularKey = ""
	case "properties":
		singularKey = "property"
	}
	itemLabel := strings.ReplaceAll(singularKey, "_", " ")
	if resource.noun == "categories" {
		itemLabel = "category"
	}
	if strings.Contains(resource.verbs, "C") {
		operations = append(operations, read(resource.noun+".get", resource.noun, "get", "Get one "+itemLabel, itemPath, singularKey, resource.access, false, parameter))
	}
	if strings.Contains(resource.verbs, "U") {
		operation := mutation(resource.noun+".create", resource.noun, "create", "Create one "+itemLabel, "POST", resource.path, singularKey, documentedResourceMutationRisk(resource.noun), resource.access, true)
		if resource.noun == "categories" {
			operation.BodyRootKeys = []string{"category"}
		}
		operations = append(operations, operation)
	}
	if strings.Contains(resource.verbs, "D") {
		operation := mutation(resource.noun+".update", resource.noun, "update", "Update one "+itemLabel, "PUT", itemPath, singularKey, documentedResourceMutationRisk(resource.noun), resource.access, true, parameter)
		if resource.noun == "categories" {
			operation.BodyRootKeys = []string{"category"}
		}
		operations = append(operations, operation)
	}
	if strings.Contains(resource.verbs, "E") {
		operations = append(operations, withOutputKind(mutation(resource.noun+".delete", resource.noun, "delete", "Delete one "+itemLabel, "DELETE", itemPath, "", RiskDelete, resource.access, false, parameter), OutputNull))
	}
	return operations
}

func read(id, noun, verb, summary, path, responseKey string, access int, paginated bool, parameters ...Parameter) Operation {
	outputKind := OutputObject
	if paginated {
		outputKind = OutputArray
	}
	return Operation{ID: id, Noun: noun, Verb: verb, Summary: summary, Method: "GET", Path: path, ResponseKey: responseKey, OutputKind: outputKind, Risk: RiskRead, RequiredAccessLevel: access, Paginated: paginated, PracticeSupported: true, PathParameters: parameters}
}

func mutation(id, noun, verb, summary, method, path, responseKey string, risk Risk, access int, bodyRequired bool, parameters ...Parameter) Operation {
	var bodyRootKeys []string
	if bodyRequired && responseKey != "" {
		bodyRootKeys = []string{responseKey}
	}
	return Operation{ID: id, Noun: noun, Verb: verb, Summary: summary, Method: method, Path: path, ResponseKey: responseKey, OutputKind: OutputObject, Risk: risk, RequiredAccessLevel: access, BodyAllowed: bodyRequired, BodyRequired: bodyRequired, BodyRootKeys: bodyRootKeys, PracticeSupported: true, PathParameters: parameters}
}

func documentedResourceMutationRisk(noun string) Risk {
	switch noun {
	case "bank-transaction-explanations", "bills", "credit-note-reconciliations", "credit-notes", "estimates", "expenses", "invoices", "journal-sets", "price-list-items", "properties":
		return RiskFinancial
	case "bank-accounts", "capital-asset-types", "categories", "users":
		return RiskControl
	default:
		return RiskWrite
	}
}

func withOutputKind(operation Operation, outputKind OutputKind) Operation {
	operation.OutputKind = outputKind
	return operation
}

func withAccessRequirement(operation Operation, requirement AccessRequirement) Operation {
	operation.RequiredAccessLevel = 0
	operation.AccessRequirement = requirement
	return operation
}

func withQueryParameters(operation Operation, parameters ...Parameter) Operation {
	operation.QueryParameters = append([]Parameter(nil), parameters...)
	return operation
}

func withBodyRootKeys(operation Operation, keys ...string) Operation {
	operation.BodyRootKeys = append([]string(nil), keys...)
	return operation
}

func withoutBody(operation Operation) Operation {
	operation.BodyAllowed = false
	operation.BodyRequired = false
	operation.BodyRootKeys = nil
	return operation
}

func applyDocumentedMetadata(operation *Operation) {
	switch operation.ID {
	case "attachments.get", "attachments.delete":
		operation.RequiredAccessLevel = 0
		operation.AccessRequirement = AccessConditional
	case "bank-transactions.list", "bank-transaction-explanations.list":
		operation.QueryParameters = []Parameter{
			QueryParameter("bank_account", "Bank account URL used to select transactions", TypeString, true),
		}
	case "notes.list", "notes.create":
		operation.QueryParameters = []Parameter{
			QueryParameter("contact", "Contact URL that owns the note", TypeString, false),
			QueryParameter("project", "Project URL that owns the note", TypeString, false),
		}
		operation.QueryConstraints = []QueryConstraint{ExactlyOneQueryParameter("contact", "project")}
		if operation.ID == "notes.create" {
			operation.BodyRootKeys = []string{"note"}
		}
	case "tasks.create":
		operation.QueryParameters = []Parameter{
			QueryParameter("project", "Project URL that owns the task", TypeString, true),
		}
		operation.BodyRootKeys = []string{"task"}
	case "users.create":
		operation.RequiredAccessLevel = 1
	}
}

func humanize(value string) string { return strings.ReplaceAll(value, "-", " ") }

func reportOperations() []Operation {
	return []Operation{
		read("reports.balance-sheet", "reports", "balance-sheet", "Get the balance sheet", "/v2/accounting/balance_sheet", "balance_sheet", 7, false),
		read("reports.balance-sheet-opening-balances", "reports", "balance-sheet-opening-balances", "Get balance sheet opening balances", "/v2/accounting/balance_sheet/opening_balances", "balance_sheet", 7, false),
		withQueryParameters(
			read("reports.cashflow", "reports", "cashflow", "Get the cashflow report", "/v2/cashflow", "cashflow", 6, false),
			QueryParameter("from_date", "Start date for the report in YYYY-MM-DD format", TypeString, false),
			QueryParameter("to_date", "End date for the report in YYYY-MM-DD format", TypeString, false),
		),
		read("reports.profit-and-loss", "reports", "profit-and-loss", "Get the profit and loss report", "/v2/accounting/profit_and_loss/summary", "profit_and_loss_summary", 7, false),
		withOutputKind(read("reports.trial-balance", "reports", "trial-balance", "Get the trial balance summary", "/v2/accounting/trial_balance/summary", "trial_balance_summary", 7, false), OutputArray),
		withOutputKind(read("reports.trial-balance-opening-balances", "reports", "trial-balance-opening-balances", "Get trial balance opening balances", "/v2/accounting/trial_balance/summary/opening_balances", "trial_balance_summary", 7, false), OutputArray),
		read("transactions.list", "transactions", "list", "List accounting transactions", "/v2/accounting/transactions", "transactions", 7, true),
		read("transactions.get", "transactions", "get", "Get one accounting transaction", "/v2/accounting/transactions/{id}", "transaction", 7, false, PathParameter("id", "Exact transaction ID")),
		read("final-accounts-reports.list", "final-accounts-reports", "list", "List final accounts reports", "/v2/final_accounts_reports", "final_accounts_reports", 7, true),
		read("final-accounts-reports.get", "final-accounts-reports", "get", "Get one final accounts report", "/v2/final_accounts_reports/{period_ends_on}", "final_accounts_report", 7, false, PathParameter("period_ends_on", "Report period end date in YYYY-MM-DD format")),
		mutation("final-accounts-reports.mark-as-filed", "final-accounts-reports", "mark-as-filed", "Set mark as filed for one final accounts report", "PUT", "/v2/final_accounts_reports/{period_ends_on}/mark_as_filed", "final_accounts_report", RiskFinancial, 8, false, PathParameter("period_ends_on", "Report period end date in YYYY-MM-DD format")),
		mutation("final-accounts-reports.mark-as-unfiled", "final-accounts-reports", "mark-as-unfiled", "Set mark as unfiled for one final accounts report", "PUT", "/v2/final_accounts_reports/{period_ends_on}/mark_as_unfiled", "final_accounts_report", RiskFinancial, 8, false, PathParameter("period_ends_on", "Report period end date in YYYY-MM-DD format")),
	}
}

func invoiceOperations() []Operation {
	return []Operation{
		read("invoices.pdf", "invoices", "pdf", "Get an invoice PDF as a base64 JSON payload", "/v2/invoices/{id}/pdf", "pdf", 4, false, PathParameter("id", "Exact invoice ID")),
		read("invoices.timeline", "invoices", "timeline", "Get the invoice timeline", "/v2/invoices/timeline", "", 4, false),
		withOutputKind(read("invoices.default-additional-text", "invoices", "default-additional-text", "Get default invoice additional text", "/v2/invoices/default_additional_text", "default_additional_text", 4, false), OutputString),
		mutation("invoices.duplicate", "invoices", "duplicate", "Duplicate an invoice", "POST", "/v2/invoices/{id}/duplicate", "invoice", RiskFinancial, 4, false, PathParameter("id", "Exact invoice ID")),
		mutation("invoices.send-email", "invoices", "send-email", "Send an invoice by email", "POST", "/v2/invoices/{id}/send_email", "invoice", RiskSend, 4, true, PathParameter("id", "Exact invoice ID")),
		withOutputKind(withoutBody(mutation("invoices.create-direct-debit", "invoices", "create-direct-debit", "Create an invoice direct debit", "POST", "/v2/invoices/{id}/direct_debit", "", RiskFinancial, 4, false, PathParameter("id", "Exact invoice ID"))), OutputNull),
		transition("invoices", "mark-as-sent", "Mark an invoice as sent", RiskFinancial),
		transition("invoices", "mark-as-scheduled", "Mark an invoice as scheduled", RiskFinancial),
		transition("invoices", "mark-as-draft", "Mark an invoice as draft", RiskFinancial),
		transition("invoices", "mark-as-cancelled", "Mark an invoice as cancelled", RiskFinancial),
		transition("invoices", "convert-to-credit-note", "Convert an invoice to a credit note", RiskFinancial),
		withOutputKind(withBodyRootKeys(mutation("invoices.update-default-additional-text", "invoices", "update-default-additional-text", "Update default invoice additional text", "PUT", "/v2/invoices/default_additional_text", "default_additional_text", RiskFinancial, 4, true), "default_additional_text"), OutputString),
		withOutputKind(mutation("invoices.delete-default-additional-text", "invoices", "delete-default-additional-text", "Delete default invoice additional text", "DELETE", "/v2/invoices/default_additional_text", "", RiskDelete, 4, false), OutputNull),
	}
}

func estimateOperations() []Operation {
	return []Operation{
		read("estimates.pdf", "estimates", "pdf", "Get an estimate PDF as a base64 JSON payload", "/v2/estimates/{id}/pdf", "pdf", 4, false, PathParameter("id", "Exact estimate ID")),
		withOutputKind(read("estimates.default-additional-text", "estimates", "default-additional-text", "Get default estimate additional text", "/v2/estimates/default_additional_text", "default_additional_text", 4, false), OutputString),
		mutation("estimates.duplicate", "estimates", "duplicate", "Duplicate an estimate", "POST", "/v2/estimates/{id}/duplicate", "estimate", RiskFinancial, 4, false, PathParameter("id", "Exact estimate ID")),
		mutation("estimates.send-email", "estimates", "send-email", "Send an estimate by email", "POST", "/v2/estimates/{id}/send_email", "estimate", RiskSend, 4, true, PathParameter("id", "Exact estimate ID")),
		estimateTransition("mark-as-approved", "Mark an estimate as approved", RiskFinancial),
		estimateTransition("mark-as-draft", "Mark an estimate as draft", RiskFinancial),
		estimateTransition("mark-as-rejected", "Mark an estimate as rejected", RiskFinancial),
		estimateTransition("mark-as-sent", "Mark an estimate as sent", RiskFinancial),
		estimateTransition("convert-to-invoice", "Convert an estimate to an invoice", RiskFinancial),
		withOutputKind(withBodyRootKeys(mutation("estimates.update-default-additional-text", "estimates", "update-default-additional-text", "Update default estimate additional text", "PUT", "/v2/estimates/default_additional_text", "default_additional_text", RiskFinancial, 4, true), "default_additional_text"), OutputString),
		withOutputKind(mutation("estimates.delete-default-additional-text", "estimates", "delete-default-additional-text", "Delete default estimate additional text", "DELETE", "/v2/estimates/default_additional_text", "", RiskDelete, 4, false), OutputNull),
	}
}

func estimateItemOperations() []Operation {
	id := PathParameter("id", "Exact estimate item ID")
	return []Operation{
		withBodyRootKeys(mutation("estimate-items.create", "estimate-items", "create", "Create one estimate item", "POST", "/v2/estimate_items", "estimate_item", RiskFinancial, 4, true), "estimate", "estimate_item"),
		withOutputKind(withBodyRootKeys(mutation("estimate-items.update", "estimate-items", "update", "Update one estimate item", "PUT", "/v2/estimate_items/{id}", "", RiskFinancial, 4, true, id), "estimate_item"), OutputNull),
		withOutputKind(mutation("estimate-items.delete", "estimate-items", "delete", "Delete one estimate item", "DELETE", "/v2/estimate_items/{id}", "", RiskDelete, 4, false, id), OutputNull),
	}
}

func creditNoteOperations() []Operation {
	return []Operation{
		read("credit-notes.pdf", "credit-notes", "pdf", "Get a credit note PDF as a base64 JSON payload", "/v2/credit_notes/{id}/pdf", "pdf", 4, false, PathParameter("id", "Exact credit note ID")),
		mutation("credit-notes.send-email", "credit-notes", "send-email", "Send a credit note by email", "POST", "/v2/credit_notes/{id}/send_email", "credit_note", RiskSend, 4, true, PathParameter("id", "Exact credit note ID")),
		creditNoteTransition("mark-as-sent", "Mark a credit note as sent"),
		creditNoteTransition("mark-as-draft", "Mark a credit note as draft"),
	}
}

func transition(noun, verb, summary string, risk Risk) Operation {
	return mutation(noun+"."+verb, noun, verb, summary, "PUT", "/v2/"+strings.ReplaceAll(noun, "-", "_")+"/{id}/transitions/"+strings.ReplaceAll(verb, "-", "_"), strings.TrimSuffix(strings.ReplaceAll(noun, "-", "_"), "s"), risk, 4, false, PathParameter("id", "Exact "+humanize(noun)+" ID"))
}

func estimateTransition(verb, summary string, risk Risk) Operation {
	return transition("estimates", verb, summary, risk)
}

func creditNoteTransition(verb, summary string) Operation {
	return transition("credit-notes", verb, summary, RiskFinancial)
}

func taxOperations() []Operation {
	var operations []Operation
	operations = append(operations, periodReturnOperations("corporation-tax-returns", "/v2/corporation_tax_returns", "corporation_tax_returns", false)...)
	operations = append(operations, periodReturnOperations("vat-returns", "/v2/vat_returns", "vat_returns", true)...)

	userID := PathParameter("user_id", "Exact FreeAgent user ID")
	period := PathParameter("period_ends_on", "Return period end date in YYYY-MM-DD format")
	payment := PathParameter("payment_date", "Payment date in YYYY-MM-DD format")
	base := "/v2/users/{user_id}/self_assessment_returns"
	operations = append(operations,
		read("self-assessment-returns.list", "self-assessment-returns", "list", "List self-assessment returns", base, "self_assessment_returns", 7, true, userID),
		read("self-assessment-returns.get", "self-assessment-returns", "get", "Get a self-assessment return", base+"/{period_ends_on}", "self_assessment_return", 7, false, userID, period),
		taxAction("self-assessment-returns", "mark-as-filed", base+"/{period_ends_on}/mark_as_filed", userID, period),
		taxAction("self-assessment-returns", "mark-as-unfiled", base+"/{period_ends_on}/mark_as_unfiled", userID, period),
		taxAction("self-assessment-returns", "mark-as-paid", base+"/{period_ends_on}/payments/{payment_date}/mark_as_paid", userID, period, payment),
		taxAction("self-assessment-returns", "mark-as-unpaid", base+"/{period_ends_on}/payments/{payment_date}/mark_as_unpaid", userID, period, payment),
	)
	return operations
}

func periodReturnOperations(noun, path, responseKey string, payments bool) []Operation {
	period := PathParameter("period_ends_on", "Return period end date in YYYY-MM-DD format")
	operations := []Operation{
		read(noun+".list", noun, "list", "List "+humanize(noun), path, responseKey, 7, true),
		read(noun+".get", noun, "get", "Get one "+humanize(noun), path+"/{period_ends_on}", strings.TrimSuffix(responseKey, "s"), 7, false, period),
		taxAction(noun, "mark-as-filed", path+"/{period_ends_on}/mark_as_filed", period),
		taxAction(noun, "mark-as-unfiled", path+"/{period_ends_on}/mark_as_unfiled", period),
	}
	if noun == "corporation-tax-returns" {
		operations = append(operations,
			taxAction(noun, "mark-as-paid", path+"/{period_ends_on}/mark_as_paid", period),
			taxAction(noun, "mark-as-unpaid", path+"/{period_ends_on}/mark_as_unpaid", period),
		)
	}
	if payments {
		payment := PathParameter("payment_date", "Payment date in YYYY-MM-DD format")
		operations = append(operations,
			taxAction(noun, "mark-as-paid", path+"/{period_ends_on}/payments/{payment_date}/mark_as_paid", period, payment),
			taxAction(noun, "mark-as-unpaid", path+"/{period_ends_on}/payments/{payment_date}/mark_as_unpaid", period, payment),
		)
	}
	return operations
}

func taxAction(noun, verb, path string, parameters ...Parameter) Operation {
	responseKey := map[string]string{
		"corporation-tax-returns": "corporation_tax_return",
		"self-assessment-returns": "self_assessment_return",
		"vat-returns":             "vat_return",
	}[noun]
	access := 7
	if verb == "mark-as-filed" || verb == "mark-as-unfiled" {
		access = 8
	}
	return mutation(noun+"."+verb, noun, verb, "Set "+humanize(verb)+" for one "+humanize(noun), "PUT", path, responseKey, RiskFinancial, access, false, parameters...)
}

func payrollOperations() []Operation {
	year := PathParameter("year", "Payroll tax year")
	period := PathParameter("period", "Payroll period")
	payment := PathParameter("payment_date", "Payment date in YYYY-MM-DD format")
	return []Operation{
		read("payroll.get-year", "payroll", "get-year", "Get payroll for a tax year", "/v2/payroll/{year}", "", 7, false, year),
		read("payroll.get-period", "payroll", "get-period", "Get one payroll period", "/v2/payroll/{year}/{period}", "period", 7, false, year, period),
		withOutputKind(read("payroll-profiles.get", "payroll-profiles", "get", "Get payroll profiles for a tax year", "/v2/payroll_profiles/{year}", "profiles", 7, false, year), OutputArray),
		// The current docs show mark-as-unpaid as GET. It is intentionally not
		// exposed as a mutation until FreeAgent confirms the route.
		withOutputKind(mutation("payroll.mark-as-paid", "payroll", "mark-as-paid", "Mark a payroll payment as paid", "PUT", "/v2/payroll/{year}/payments/{payment_date}/mark_as_paid", "", RiskFinancial, 7, false, year, payment), OutputNull),
	}
}

func practiceOperations() []Operation {
	id := PathParameter("id", "Exact account manager ID")
	operations := []Operation{
		read("practice.get", "practice", "get", "Get the accountancy practice", "/v2/practice", "", 0, false),
		read("practice-clients.list", "practice-clients", "list", "List practice clients", "/v2/clients", "clients", 0, true),
		read("account-managers.list", "account-managers", "list", "List practice account managers", "/v2/account_managers", "account_managers", 0, true),
		read("account-managers.get", "account-managers", "get", "Get one practice account manager", "/v2/account_managers/{id}", "account_manager", 0, false, id),
		read("account-managers.me", "account-managers", "me", "Get the current practice account manager", "/v2/account_managers/me", "account_manager", 0, false),
	}
	for index := range operations {
		operations[index].AccessRequirement = AccessAccountManager
		operations[index].PracticeOnly = true
		operations[index].PracticeSupported = true
		if operations[index].ID == "practice-clients.list" {
			operations[index].MaxPerPage = 500
			operations[index].QueryParameters = []Parameter{
				QueryParameter("minimal_data", "When true, request up to 500 clients per page; otherwise the maximum is 100", TypeBoolean, false),
			}
		}
	}
	return operations
}

func documentationURL(noun, verb string) string {
	slug := strings.ReplaceAll(noun, "-", "_")
	switch noun {
	case "account-managers", "practice", "practice-clients":
		slug = "accountancy_practice_api"
	case "estimate-items":
		slug = "estimates"
	case "reports":
		switch {
		case strings.HasPrefix(verb, "balance-sheet"):
			slug = "balance_sheet"
		case verb == "cashflow":
			slug = "cashflow"
		case verb == "profit-and-loss":
			slug = "profit_and_loss"
		default:
			slug = "trial_balance"
		}
	case "self-assessment-returns":
		slug = "income_tax_returns"
	case "sales-tax":
		slug = "sales_tax"
	}
	return "https://dev.freeagent.com/docs/" + slug
}
