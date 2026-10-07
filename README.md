# purelymail-cli

A command line interface to the [Purelymail](https://purelymail.com) API, generated from
Purelymail's own OpenAPI spec.

All 19 API operations, every request body field typed, marked required or optional, and
described, because the commands are not written by hand. `tools/gen_command_tree.py` reads
the spec Purelymail publishes with its [API docs](https://news.purelymail.com/api/index.html)
and emits a schema the Go binary embeds. When Purelymail adds an endpoint, you regenerate
rather than catch up.

Built to be scripted against and called by language models: JSON by default, structured
discovery commands, real exit codes, a `--dry-run` that shows the request without sending
it, and destructive operations that refuse to run without `--yes`.

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/KudcraftsHQ/purelymail-cli/main/scripts/install.sh | bash
```

Or grab a tarball from [releases](https://github.com/KudcraftsHQ/purelymail-cli/releases),
or build it yourself:

```bash
go install github.com/KudcraftsHQ/purelymail-cli/cmd/purelymail@latest
# or, from a checkout
make install            # into $GOBIN
make build              # into bin/purelymail
```

## Authenticate

Create an API token in Purelymail (Settings, API tokens), then:

```bash
export PURELYMAIL_API_TOKEN="pm-live-..."
```

For more than one account, use profiles in `~/.config/purelymail/config.toml`:

```toml
api_token = "pm-live-..."

[client-b]
api_token = "pm-live-..."
```

```bash
purelymail --profile client-b domain list
```

`--token` beats `PURELYMAIL_API_TOKEN`, which beats the config file. `PURELYMAIL_PROFILE`
selects a profile, `PURELYMAIL_CONFIG` moves the file. Check what resolved, and that it
works:

```bash
purelymail config show       # never prints the token, only where it came from
purelymail config verify     # calls checkAccountCredit (read-only) and prints the credit
```

`config verify` exits `0` when the token works, `1` when none is configured or the API could
not be reached, and `2` when Purelymail refused the token.

## Find your way around

```bash
purelymail list                    # resources and their operations
purelymail list domain             # each command and the API operation it calls
purelymail describe domain update  # body fields, types, required, defaults, result shape
purelymail describe addDomain      # the API's own operation names work too
purelymail tree                    # the whole catalog as JSON
```

```
$ purelymail describe routing create
POST https://purelymail.com/api/v0/createRoutingRule  (createRoutingRule)
Creates a new routing rule for a domain. Routing rule must not have the same user/prefix as any other existing rules for the domain.

  purelymail routing create --domain-name <domainName> --prefix=false --match-user <matchUser> --target-addresses <value>

FLAG                BODY FIELD       TYPE           REQUIRED  DEFAULT  DESCRIPTION
--domain-name       domainName       string         yes
--prefix            prefix           boolean        yes
--match-user        matchUser        string         yes                The local part of the user address to be matched, ...
--target-addresses  targetAddresses  array<string>  yes                List of full email addresses that this mail will be rerouted to
--catchall          catchall         boolean        no        false    Whether this is a 'catchall' rule that will not fire if address maps to a user
```

## Use it

| Resource | Commands | API operations |
|---|---|---|
| `user` | `create` `get` `list` `modify` `delete` | createUser getUser listUser modifyUser deleteUser |
| `password-reset` | `upsert` `list` `delete` | upsertPasswordReset listPasswordReset deletePasswordReset |
| `app-password` | `create` `delete` | createAppPassword deleteAppPassword |
| `routing` | `create` `list` `delete` | createRoutingRule listRoutingRules deleteRoutingRule |
| `domain` | `add` `list` `update` `ownership-code` `delete` `dns` | addDomain listDomains updateDomainSettings getOwnershipCode deleteDomain |
| `account` | `credit` | checkAccountCredit |

```bash
# Domains
purelymail domain list -o table
purelymail domain list --include-shared
purelymail domain ownership-code
purelymail domain add example.com
purelymail domain update example.com --recheck-dns
purelymail domain delete example.com --yes

# Users
purelymail user create --user-name hello --domain-name example.com --password '...' --send-welcome-email=false
purelymail user get hello@example.com
purelymail user modify hello@example.com --require-two-factor-authentication
purelymail app-password create hello@example.com --name "Thunderbird"

# Routing: forward hello@ to two addresses
purelymail routing create --domain-name example.com --prefix=false --match-user hello \
  --target-addresses a@gmail.com --target-addresses b@gmail.com
purelymail routing list -o table
purelymail routing delete 1234 --yes

# Anything the generated tree does not cover
purelymail raw listDomains --body '{"includeShared":true}'
```

Body fields are flags (`userName` becomes `--user-name`). Booleans take `--flag` or
`--flag=false`; arrays take the flag repeated, comma-separated values, or a JSON array. When
an operation has exactly one required field it may be the first argument
(`domain add example.com`). `--body '<json>'` or `-f body.json` (`-f -` for stdin) supplies
the whole body, and explicit flags win over it. Unset flags are never sent, so
`user modify` changes only what you name.

### DNS records for a domain

```bash
purelymail domain dns example.com            # JSON array, ready for a DNS provider's API
purelymail domain dns example.com -o table
purelymail domain dns example.com --skip-ownership   # offline, no token needed
```

Prints the MX (`mailserver.purelymail.com`, priority 50), SPF
(`v=spf1 include:_spf.purelymail.com ~all`), the three DKIM CNAMEs
(`purelymail{1,2,3}._domainkey` to `key{1,2,3}.dkimroot.purelymail.com`), the DMARC CNAME
(`_dmarc` to `dmarcroot.purelymail.com`) and the ownership TXT from `getOwnershipCode`, with
fully qualified names. The values follow Purelymail's
[domain docs](https://purelymail.com/docs/domainDocs). On Cloudflare the CNAMEs must be
DNS only (not proxied), and an existing SPF record needs merging rather than a second one.

## Flags worth knowing

| Flag | |
|---|---|
| `--dry-run` | Print the request that would be sent, token redacted. Nothing goes out, and no token is needed. |
| `-o, --output` | `json` (default), `ndjson`, `table`, `csv`. List results render as rows. |
| `--pretty` | Indent JSON |
| `--columns` | Column order for table and csv |
| `--body`, `-f` | Whole request body as JSON, inline or from a file |
| `--yes` | Required by every `delete` (and `raw delete*`) |
| `-v, --verbose` | Log requests to stderr |
| `--max-retries` | Retries for 429/502/503/504 only (default 2). A 500 is never retried: the create may have happened. |
| `--profile`, `--token` | Which credential to use |

Exit codes, so scripts can branch:

| | |
|---|---|
| `0` | success |
| `1` | generic failure, usage error, missing token, refused for lack of `--yes` |
| `2` | Purelymail refused the token (`invalidToken`) |
| `3` | Purelymail rejected the request (any other `{"type":"error"}`) |
| `4` | rate limited |
| `5` | server-side or transient |
| `6` | not found |

Purelymail answers HTTP 200 for both outcomes and puts the verdict in the body
(`{"type":"success","result":{...}}` or `{"type":"error","code":"...","message":"..."}`).
The CLI prints only `result` on success. On failure it prints the code and message to stderr,
leaves stdout empty and exits with one of the codes above.

## For AI agents

Everything an agent needs to drive this is discoverable from the binary itself:

1. `purelymail list -o json` lists the resources and their command names.
2. `purelymail describe <resource> <op> -o json` returns the full contract: the endpoint
   and every body field (`name`, `flag`, `type`, `kind`, `required`, `default`,
   `description`), the positional argument if any, whether it is `destructive`, and
   `returns`, the result shape as dotted paths (`domains[].dnsSummary.passesMx`).
3. `purelymail tree` returns the same for every operation in one document.
4. Before a write, run it with `--dry-run` to see the exact JSON body. It needs no token.
5. Branch on the exit code, not on parsing stderr. stdout holds only the result.

Discovery commands print aligned text by default and JSON when `-o json` is given. API
commands print JSON by default. Deletes refuse to run without `--yes`; do not add it
unless the user asked for that deletion.

## Regenerating the command tree

The generated schema (`internal/tree/schema.json`) is committed, so building needs only Go.
Python is needed only to regenerate:

```bash
make tree      # fetches the spec into .spec/ (gitignored), regenerates the schema
make test
```

The generator fails if the spec gains an operation it has no command name for, so a new
endpoint cannot vanish silently. Map it in `OPS` in `tools/gen_command_tree.py`. CI
regenerates on every run and warns when the committed tree has drifted.

## How it is put together

```
tools/gen_command_tree.py   Purelymail's OpenAPI spec -> internal/tree/schema.json
internal/tree/              embeds the schema; lookup and suggestions
internal/api/               HTTP, the response envelope, retries, exit-code mapping
internal/dispatch/          schema -> cobra commands, flag coercion, discovery, domain dns
internal/config/            token resolution and profiles
internal/output/            json, ndjson, table, csv
```

The structure, idioms and output layer follow
[meta-ads-cli](https://github.com/KudcraftsHQ/meta-ads-cli).

## License

MIT for the code in this repository.

The API, and the OpenAPI spec it is generated from, belong to Purelymail. The spec is
published under a proprietary license with the
[API documentation](https://news.purelymail.com/api/index.html), so it is not
redistributed here: `make spec` downloads it at build time. The committed schema holds only
what the CLI needs to describe the API: operation names, paths, body field names, types and
their one-line descriptions. This project is not affiliated with Purelymail.
