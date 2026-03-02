# Template Functions

Runway templates use Go [`text/template`](https://pkg.go.dev/text/template) syntax with the full [Sprig v3](https://masterminds.github.io/sprig/) function library (70+ functions) plus Runway-specific helpers.

## Runway-Specific Functions

| Function | Signature | Description | Example |
|----------|-----------|-------------|---------|
| `json` | `json(v any) string` | Marshal a value to a JSON string | `{{json .PathParams}}` |
| `first` | `first(vals []string) string` | Return the first element of a string slice, or `""` if empty | `{{first (.Query "tags")}}` |

## Sprig Functions by Category

The tables below highlight the most useful Sprig functions for gateway templates. For the complete list, see the [Sprig documentation](https://masterminds.github.io/sprig/).

### String Functions

| Function | Description | Example |
|----------|-------------|---------|
| `trim` | Remove leading/trailing whitespace | `{{trim .Headers.Get "X-Name"}}` |
| `upper` / `lower` | Change case | `{{upper .Method}}` |
| `replace` | Replace substring | `{{replace "-" "_" .RouteID}}` |
| `contains` | Test if string contains substring | `{{if contains "admin" .Path}}...{{end}}` |
| `hasPrefix` / `hasSuffix` | Test string prefix/suffix | `{{if hasPrefix "/api" .Path}}...{{end}}` |
| `trunc` | Truncate to length | `{{trunc 64 .Body}}` |
| `nospace` | Remove all whitespace | `{{nospace .Body}}` |
| `quote` | Wrap in double quotes | `{{quote .ClientIP}}` |
| `substr` | Extract substring | `{{substr 0 8 .RequestID}}` |

### String List Functions

| Function | Description | Example |
|----------|-------------|---------|
| `join` | Join list with separator | `{{join "," .Tags}}` |
| `split` | Split string into map | `{{split "/" .Path}}` |
| `splitList` | Split string into list | `{{splitList "," .Headers.Get "Accept"}}` |
| `sortAlpha` | Sort string list alphabetically | `{{sortAlpha .Items}}` |

### Math Functions

| Function | Description | Example |
|----------|-------------|---------|
| `add` | Addition | `{{add .StatusCode 1000}}` |
| `sub` | Subtraction | `{{sub .Total .Used}}` |
| `mul` / `div` | Multiply / divide | `{{mul .Count 100}}` |
| `max` / `min` | Maximum / minimum | `{{max .Retries 1}}` |
| `ceil` / `floor` / `round` | Rounding | `{{round .Score 2}}` |

### Type Conversion Functions

| Function | Description | Example |
|----------|-------------|---------|
| `atoi` | String to int | `{{atoi (.Headers.Get "X-Count")}}` |
| `int64` | Convert to int64 | `{{int64 .Port}}` |
| `float64` | Convert to float64 | `{{float64 .Score}}` |
| `toString` | Convert to string | `{{toString .StatusCode}}` |

### Date Functions

| Function | Description | Example |
|----------|-------------|---------|
| `now` | Current time | `{{now}}` |
| `date` | Format time | `{{now \| date "2006-01-02"}}` |
| `dateInZone` | Format time in timezone | `{{dateInZone "15:04" (now) "UTC"}}` |
| `unixEpoch` | Time to Unix timestamp | `{{now \| unixEpoch}}` |

### Default and Conditional Functions

| Function | Description | Example |
|----------|-------------|---------|
| `default` | Default value if empty | `{{default "unknown" .ClientIP}}` |
| `empty` | Test if value is empty | `{{if empty .Body}}...{{end}}` |
| `coalesce` | First non-empty value | `{{coalesce .Host .Headers.Get "X-Forwarded-Host"}}` |
| `ternary` | Conditional value | `{{ternary "yes" "no" .Enabled}}` |

### Encoding Functions

| Function | Description | Example |
|----------|-------------|---------|
| `b64enc` | Base64 encode | `{{b64enc .Body}}` |
| `b64dec` | Base64 decode | `{{b64dec .EncodedData}}` |

### Dict (Map) Functions

| Function | Description | Example |
|----------|-------------|---------|
| `dict` | Create a dictionary | `{{json (dict "ip" .ClientIP "path" .Path)}}` |
| `get` | Get value from dict | `{{get .Parsed "name"}}` |
| `set` | Set value in dict | `{{set .Parsed "timestamp" (now \| unixEpoch)}}` |
| `hasKey` | Test if key exists | `{{if hasKey .Parsed "error"}}...{{end}}` |
| `keys` / `values` | Get dict keys/values | `{{keys .PathParams}}` |
| `pick` / `omit` | Include/exclude keys | `{{json (pick .Parsed "id" "name")}}` |

### List Functions

| Function | Description | Example |
|----------|-------------|---------|
| `list` | Create a list | `{{list "a" "b" "c"}}` |
| `last` | Last element | `{{last .Items}}` |
| `append` | Append to list | `{{append .Items "new"}}` |
| `has` | Test if list contains value | `{{if has "admin" .Roles}}...{{end}}` |
| `without` | Remove elements | `{{without .Items "deprecated"}}` |
| `uniq` | Deduplicate | `{{uniq .Tags}}` |

### Regex Functions

| Function | Description | Example |
|----------|-------------|---------|
| `regexMatch` | Test regex match | `{{if regexMatch "^/api/v[0-9]+" .Path}}...{{end}}` |
| `regexFind` | Find first match | `{{regexFind "[0-9]+" .Path}}` |
| `regexReplaceAll` | Replace all matches | `{{regexReplaceAll "[^a-z]" .Input ""}}` |

### Crypto Functions

| Function | Description | Example |
|----------|-------------|---------|
| `sha256sum` | SHA-256 hash | `{{sha256sum .Body}}` |
| `sha1sum` | SHA-1 hash | `{{sha1sum .RequestID}}` |

### Other Functions

| Function | Description | Example |
|----------|-------------|---------|
| `uuidv4` | Generate a UUID v4 | `{{uuidv4}}` |
| `urlquery` | URL-encode a string | `{{urlquery .Path}}` |
| `env` | Read environment variable | `{{env "REGION"}}` |
| `toJson` / `fromJson` | Sprig JSON encode/decode | `{{toJson .Parsed}}` |

## Features Using Templates

The full Sprig + Runway function set is available in these features:

- [Body Generator](../transformations/body-generator.md) — generate request bodies from templates
- [Response Body Generator](../transformations/response-body-generator.md) — generate response bodies from templates
- [Body Transform](../transformations/transformations.md#body-transform) — transform request/response bodies with templates
- [Sequential Proxy](../traffic-routing/sequential-proxy.md) — chain backend calls with templated URLs and bodies
- [Response Aggregation](../traffic-routing/response-aggregation.md) — templated backend URLs
- [GraphQL Protocol](../protocol/graphql.md) — GraphQL query templates

### Exception: Error Pages

[Error page](../transformations/error-pages.md) templates use standard Go `text/template` only. Sprig functions are **not** available in error page templates.

## Go Template Syntax Quick Reference

| Syntax | Description |
|--------|-------------|
| `{{.Field}}` | Access a field |
| `{{.Nested.Field}}` | Access nested field |
| `{{index .Map "key"}}` | Access map value by key |
| `{{if .Cond}}...{{else}}...{{end}}` | Conditional |
| `{{range .List}}...{{end}}` | Iterate |
| `{{.Value \| func}}` | Pipeline |
| `{{- ... -}}` | Trim whitespace |

See the [Go template documentation](https://pkg.go.dev/text/template) for complete syntax details.
