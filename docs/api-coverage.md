# API coverage and limitations

The FreeAgent API is broad and changes over time. The installed operation
registry is the only authoritative list of catalogued CLI coverage:

```sh
freeagent capabilities --profile sandbox --agent
```

Use `freeagent schema OPERATION --agent` for the installed input and output
contract. Do not use this page as a substitute for discovery.

## Coverage model

The CLI groups FreeAgent operations into these domains:

- Company, users, access, and account locks.
- Contacts, invoices, estimates, credit notes, bills, expenses, and recurring
  records.
- Projects, tasks, timeslips, notes, and attachments.
- Bank accounts, transactions, explanations, feeds, and transfers.
- Categories, journals, assets, stock, properties, and accounting reports.
- VAT, corporation tax, income tax, self assessment, final accounts, payroll,
  sales tax, and CIS.
- Accountancy Practice client context.

Not every resource supports CRUD. Company type, registration, subscription,
feature enablement, and the approving user's access level can restrict an
otherwise documented operation.

Catalogued commands are added only when the route, risk class, pagination, and
documented request model are understood. Their payload schemas can remain
explicitly incomplete. A restricted relative-path `api` command provides access
to compatible uncatalogued routes without weakening host or mutation safety.

## Known API limitations

- No documented granular OAuth scopes.
- No documented OAuth PKCE, device flow, or token revocation endpoint.
- No documented idempotency-key support.
- No documented ETag or `If-Match` concurrency contract.
- No documented webhook, event, or deletion-feed contract.
- Inconsistent error body shapes.
- Mixed JSON number and decimal-string representations across some reports and
  uploads.
- Invoice, estimate, and credit-note PDF routes return base64 data inside JSON;
  this release preserves that payload and does not decode it to a file.
- Catalogued bank-statement import accepts the documented JSON `statement`
  form. This release does not construct the alternative multipart upload.
- Some logical identifiers are codes, dates, tax years, subdomains, or complete
  resource URLs rather than uniform numeric IDs.
- List pagination can drift while records are being changed concurrently.
- Some imports and financial transitions have asynchronous or lifecycle
  behaviour.

These are statements about the public documentation, not proof that an
undocumented server feature cannot exist.

## Documentation inconsistencies

The FreeAgent documentation has shown apparent path or method errors for some
task, bank-transaction, price-list, payroll, and notes operations. Such routes
remain disabled as catalogued writes until a sandbox contract test proves the
actual behaviour.

## API versioning

Each profile pins a documented `X-Api-Version` date. FreeAgent selects the
newest version available on or before that date. The configured release default
is `2024-10-01`; using the current date or a future date is not a stable pin.
An accepted header does not prove that the intended contract is active. See
[FreeAgent API versioning](https://dev.freeagent.com/docs/versioning). Version
upgrades require focused sandbox tests.

The versioned bank-transaction-explanation attachment API changes the
attachment shape and has separate count limits. Its commands must advertise
their minimum tested API version through capabilities.

## Accountancy Practice

Practice access is a separate opt-in capability. It uses an account-manager
token and `X-Subdomain` to select a client. Every output and confirmation plan
must include the practice client context. Do not reuse a confirmation token
across subdomains.

Company API coverage is the first-release priority. Treat Practice support as
unavailable unless `capabilities` explicitly reports it.
