# Response Flatmap & Target

Flatmap and target extend the body transform engine with response data shaping capabilities inspired by KrakenD's response manipulation.

## Target

The `target` field extracts a nested JSON path as the root response, discarding the wrapper object.

```yaml
routes:
  - id: api-users
    path: /api/users
    backends:
      - url: http://backend:8080
    transform:
      response:
        body:
          target: data.users
```

Given a backend response:
```json
{"status": "ok", "data": {"users": [{"id": 1}, {"id": 2}]}}
```

The gateway returns:
```json
[{"id": 1}, {"id": 2}]
```

If the target path does not exist, the original response is returned unchanged.

## Flatmap Operations

Flatmap operations manipulate arrays and values in the response body. They run after all other field operations (set/add/remove/rename) and before the template.

```yaml
routes:
  - id: api-products
    path: /api/products
    backends:
      - url: http://backend:8080
    transform:
      response:
        body:
          flatmap:
            - type: extract
              args: [items, name]
            - type: move
              args: [items, product_names]
            - type: del
              args: [debug]
```

### Supported Operations

| Type | Args | Description |
|------|------|-------------|
| `move` | [source, dest] | Move a value from source path to dest path |
| `del` | [path] | Delete a path |
| `extract` | [array_path, field] | Extract a field from each object in an array |
| `flatten` | [path] | Flatten nested arrays into a single array |
| `append` | [dest, src1, src2, ...] | Concatenate source arrays into dest |

### Extract Example

```json
// Input
{"users": [{"id": 1, "name": "alice"}, {"id": 2, "name": "bob"}]}

// flatmap: [{type: extract, args: [users, name]}]
{"users": ["alice", "bob"]}
```

### Flatten Example

```json
// Input
{"matrix": [[1, 2], [3, 4], [5]]}

// flatmap: [{type: flatten, args: [matrix]}]
{"matrix": [1, 2, 3, 4, 5]}
```

### Append Example

```json
// Input
{"page1": ["a", "b"], "page2": ["c", "d"]}

// flatmap: [{type: append, args: [all, page1, page2]}]
{"page1": ["a", "b"], "page2": ["c", "d"], "all": ["a", "b", "c", "d"]}
```

## Processing Order

The full body transform processing order is:

1. `target` — extract nested path as root
2. `allow_fields` / `deny_fields` — field filtering
3. `set_fields` — set values at paths
4. `add_fields` — add top-level fields
5. `remove_fields` — remove paths
6. `rename_fields` — rename paths
7. `flatmap` — array manipulation
8. `template` — Go template (terminal)

## Combined Example

```yaml
transform:
  response:
    body:
      target: data
      deny_fields: [internal_id, debug]
      flatmap:
        - type: extract
          args: [items, name]
        - type: flatten
          args: [tags]
```

## Admin API

Flatmap and target are part of the existing body transform pipeline. No separate admin endpoint is needed — stats are included in the route's transform metrics.
