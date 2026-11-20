# Data packages

A Greenware source can read numbers from a typed data package that lives in the project. The game keeps its field model in its own repository, and Greenware reads it with the same code the game's generator uses. Greenware ships no data of its own: the examples here use an invented package, `example/world`, from `testdata/world/`.

Decision: 0020 (hypha://m31labs/greenware). Go API: `m31labs.dev/greenware/datapkg`.

## In a source

```text
import "example/world@1" as world

let width = world.crate.width          // 24 in, as 0.6096 m
let tilt  = world.crate.tilt           // 15 deg, as radians
```

`import "<name>@<major>" as <qualifier>` brings in one package. The `@1` is the contract major the asset accepts. Without `as`, the qualifier is the last word of the name (`world`). A reference is the qualifier and the path of a field, `world.crate.width`. It evaluates to a dimensioned value:

| Dimension | Value in the source | Units a data file may use |
|---|---|---|
| `length` | a length, in metres | `mm` `cm` `m` `km` `in` `ft` `yd` |
| `angle` | an angle, in radians | `deg` `rad` |
| `ratio` | a plain number: 12.5 % is 0.125 | `%` |
| `number` | a plain number | none |

A length converts to metres exactly, as a rational number, and becomes a float64 once, at the end. The value has the same bits as the literal written in that unit (`24in`, `15deg`, `12.5%`). A value of the wrong dimension for its slot is `GW-UNIT` like any other (a length where an angle is needed).

The package is found from the project root, not from the importing file's folder, and never from outside the project. A `.gw` file import (`import "helmet.gw" as helmet`) and a library (`std/...`) are unchanged: a path with `@` and no `.gw` suffix is a data package.

## The three files

A package is a lock file, a schema file and a data file. All three are strict JSON (RFC 8259) in UTF-8. Each file is at most 2 MiB. A duplicate key, a comment, a trailing comma, a byte order mark, text after the value, a lone surrogate escape or a nesting deeper than 32 levels is an error. Nothing defaults, and no field is optional.

### Lock

`<name>.lock` under the project root: `football/world@1` is `football/world.lock`. Fields (all required, no others):

```json
{
  "schema": "greenware.datalock/1",
  "contract": "example/world@1",
  "schema_file": "example/world/schema-v1.json",
  "data_file": "example/world/standard.json",
  "sha256": "6a972715adc467fefb3e4c892958ee75a1cdb752a407b817fbd2af606e9beab5"
}
```

| Field | Meaning |
|---|---|
| `schema` | `greenware.datalock/1` |
| `contract` | the package, `<name>@<major>`; it must equal the import's text |
| `schema_file`, `data_file` | project-relative, slash-separated paths, with no `..`, no leading `/` and no backslash |
| `sha256` | 64 lower-case hex digits: the pin (see the digest below) |

`greenware lock --data` and `datapkg.WriteLock` write this file with two-space indent, the keys in the order above and a final newline. A reader accepts any key order and white space.

### Schema

```json
{
  "contract": "example/world@1",
  "fields": {
    "crate": {
      "width": {
        "dimension": "length",
        "meaning": "distance between the two outer side faces of the crate, along x"
      },
      "depth": {
        "dimension": "length",
        "meaning": "distance between the front and back outer faces of the crate, along z"
      },
      "height": {
        "dimension": "length",
        "meaning": "height of the crate from the floor to the top face"
      },
      "slat_count": {
        "dimension": "number",
        "meaning": "number of slats across the top, counted along z"
      },
      "slat_gap": {
        "dimension": "ratio",
        "meaning": "share of each slat pitch left open between neighbouring slats"
      },
      "tilt": {
        "dimension": "angle",
        "meaning": "angle of the lid about the x axis when the crate is open"
      }
    }
  }
}
```

The schema has exactly `contract` and `fields`. `contract` equals the lock's. `fields` is an object of names; a name is `[a-z_][a-z0-9_]*`, so that `world.crate.width` is one qualified name. A name holds either a field, `{"dimension": ..., "meaning": ...}` with exactly those two keys, or a group (an object of names), nested up to 8 deep. A group must not be empty, and a group cannot have a member called `dimension` or `meaning`. `dimension` is `length`, `angle`, `ratio` or `number`. `meaning` says what is measured and from what surface; it must not be blank. Greenware shows the meaning in `greenware describe` and in the language server's hover.

### Data

```json
{
  "contract": "example/world@1",
  "profile": "standard",
  "values": {
    "crate": {
      "width": { "value": "24", "unit": "in" },
      "depth": { "value": "18", "unit": "in" },
      "height": { "value": "2", "unit": "ft" },
      "slat_count": { "value": "6" },
      "slat_gap": { "value": "12.5", "unit": "%" },
      "tilt": { "value": "15", "unit": "deg" }
    }
  }
}
```

The data file has exactly `contract` (the schema's), `profile` (a non-empty string that names this set of values; it goes into the output's record) and `values`. `values` has a value for every field the schema declares and nothing else. A value is `{"value": "<decimal>", "unit": "<unit>"}` with exactly those keys; a `number` has only `value`. A decimal is a JSON string that matches `-?(0|[1-9][0-9]*)(\.[0-9]+)?` and is at most 64 characters: no `+`, no exponent, no leading zero, no bare dot, no spaces. The value is read as an exact rational number.

## The digest

The pin is the SHA-256 of these bytes:

```text
"greenware.datapkg/1"  0x00  uint64be(len(S))  S  uint64be(len(D))  D
```

`S` and `D` are the canonical forms of the schema file and the data file, `uint64be` is eight bytes, big endian, and the result is written as 64 lower-case hex digits. The canonical form of a JSON document:

1. Parse it strictly (above). Object keys are compared after escape decoding, so `"a"` and `"\u0061"` are the same key.
2. Write objects with the keys sorted by their UTF-8 bytes, no white space between tokens, and `:` and `,` as the only separators.
3. Write a string as `"`, its characters, `"`. Only three kinds of character are escaped: `"` as `\"`, `\` as `\\`, and a byte below 0x20 as `\u00xx` with lower-case hex digits (a newline is `\u000a`, not `\n`). Every other character is written as UTF-8 as it is: `<`, `>`, `&`, `/`, U+2028 and all non-ASCII text are not escaped.
4. Write a number, `true`, `false` and `null` exactly as they were written in the file. (A package has no numbers: values are strings.)

So key order and white space in the files do not change the digest, and any other edit does.

### Test vectors

A second implementation must reproduce these. The Go tests in `datapkg/datapkg_test.go` check the same strings, and a test checks that this page holds them.

The fixture files are `testdata/world/example/world/schema-v1.json` (shown above) and `standard.json` (the data file above).

Canonical schema, 667 bytes:

```text
{"contract":"example/world@1","fields":{"crate":{"depth":{"dimension":"length","meaning":"distance between the front and back outer faces of the crate, along z"},"height":{"dimension":"length","meaning":"height of the crate from the floor to the top face"},"slat_count":{"dimension":"number","meaning":"number of slats across the top, counted along z"},"slat_gap":{"dimension":"ratio","meaning":"share of each slat pitch left open between neighbouring slats"},"tilt":{"dimension":"angle","meaning":"angle of the lid about the x axis when the crate is open"},"width":{"dimension":"length","meaning":"distance between the two outer side faces of the crate, along x"}}}}
```

Canonical data, 278 bytes:

```text
{"contract":"example/world@1","profile":"standard","values":{"crate":{"depth":{"unit":"in","value":"18"},"height":{"unit":"ft","value":"2"},"slat_count":{"value":"6"},"slat_gap":{"unit":"%","value":"12.5"},"tilt":{"unit":"deg","value":"15"},"width":{"unit":"in","value":"24"}}}}
```

Digest of the two:

```text
6a972715adc467fefb3e4c892958ee75a1cdb752a407b817fbd2af606e9beab5
```

The smallest case: the schema `{}` and the data `{}` (the digest does not check the contents, only that both are JSON), whatever the white space:

```text
4ae6067eb3d076297c552218bd57421b6c41dedc855d268a74672633c70ece3e
```

Canonical form of a string-escape case. Both texts are shown as Go interpreted string literals, so every character is unambiguous (`\u2028` and `\u00e9` in the Go literal are the characters themselves; `\\u00e9` is a JSON escape that the parser decodes):

```go
in   := "{ \"b\" : \"a\\u0001\\\"\\\\\\n<>&\\/\u2028\\u00e9\\ud83d\\ude00\",\n \"a\":[1.50, true,null,-0,1E+2],\"\u00e9\":{},\"B\":false }"
want := "{\"B\":false,\"a\":[1.50,true,null,-0,1E+2],\"b\":\"a\\u0001\\\"\\\\\\u000a<>&/\u2028\u00e9\U0001F600\",\"\u00e9\":{}}"
```

The input has an escaped quote, backslash, newline, control character, solidus and surrogate pair; U+2028 and `\u00e9` as raw characters and as escapes; numbers `1.50`, `-0` and `1E+2`; and keys `b`, `a`, `\u00e9` and `B`. The output sorts the keys by UTF-8 bytes (`B`, `a`, `b`, `\u00e9`), writes the newline as `\u000a`, drops the escape of `/`, writes the surrogate pair as the one character U+1F600, keeps U+2028 and `\u00e9` raw, and keeps the numbers as written.

## Resolution and checks

On every build, for each `import "<name>@<N>"`:

1. The name must be lower-case words of `[a-z0-9_-]` joined by `/`, then `@` and a major without a leading zero. The lock is `<root>/<name>.lock`, where the root is the folder with `greenware.mod` (or the source's own folder when there is none), the same root as a project file import. A web build reads the lock, schema and data file from the sealed snapshot of the request and from nowhere else.
2. The lock's `contract` must equal `<name>@<N>`. If not, `GW-IMPORT-CONTRACT`.
3. The schema and data file are read and parsed, the schema's and the data's `contract` must equal the lock's (`GW-IMPORT-CONTRACT` if not), and the data must match the schema (`GW-IMPORT-DATA`; a unit of another dimension is `GW-UNIT`).
4. The digest of the files is compared with the lock's `sha256`. A difference is `GW-LOCK`, with the expected and the actual digest. Nothing is built and nothing is written.

Checks 2 to 4 run in that order, so a malformed file is reported before a stale pin. Nothing is fetched from anywhere, and nothing is defaulted, rounded or guessed along the way.

## Errors

Every error is a diagnostic at the import's path string in the `.gw` file.

| Code | When | Message names |
|---|---|---|
| `GW-IMPORT-DATA` | a file cannot be read or parsed; a field the schema does not declare or lacks; a value that is not a decimal string; a missing or unknown unit; a bad dimension or name | the file and the JSON path, for example `$.values.crate.width.value`, and the line and column of a syntax error |
| `GW-UNIT` | a unit of another dimension than the field's (`mm` for an angle) | the file and the JSON path |
| `GW-IMPORT-CONTRACT` | the import's major does not match the lock, or the lock, the schema and the data name different contracts | both contracts |
| `GW-LOCK` | no lock file, or the pin does not match the files | the expected and the actual digest |

In the source, a path that is not a field is `GW-UNKNOWN-NAME` at the wrong name, with the nearest field name as the fix. A group where a value is needed (`world.crate`) is `GW-TYPE`.

## The lock workflow

1. Edit the schema or the data file. A build now fails with `GW-LOCK`.
2. Review the change, then run `greenware lock --data example/world@1 [<root>]`. It parses both files, checks them against each other and writes the new pin. The first lock of a package names the files: `--schema-file example/world/schema-v1.json --data-file example/world/standard.json`.
3. Run `greenware lock [<root>]` to record the package in `greenware.sum` as `data <name@N> contract <id> sha256:<digest>`. `greenware lock --check` flags a pin that is behind.

Only `greenware lock --data` changes a pin. A build never does. `datapkg.Load(root, "football/world@1")` runs checks 1 to 4 for a game's generator, so the generator and the bake use the same code: `Package.Value("goal_post", "inside_width")` returns the field, its meaning and its exact value (`Value.Exact()` gives a `*big.Rat` in metres for a length). `datapkg.Digest`, `Verify`, `WriteLock` and `NewLock` are the other entry points.

## What the output records

Each GLB of an asset that imports a package carries, in `asset.extras`:

```json
{"greenware":{"inputs":[{"contract":"example/world@1","profile":"standard","sha256":"6a97..."}]}}
```

The list is sorted by contract, and `profile` is the data file's. The build record has the same list at `inputs.data`. The digest is also part of the build's input hash and of each output's `fir_sha256`, so a changed value changes both. An asset that imports no package has none of this, and its bytes are unchanged.

## What Greenware does not do

- It carries no football data and no real-world dimensions. The package belongs to the project.
- It does not fetch, download or search for a package, and it never changes a pin by itself.
- It does not convert between dimensions (no length to angle), and it does not guess a unit: a unit is required for every dimension but `number`.
- It does not round before the end: exact rational arithmetic until the one conversion to float64.
- It does not read Arbiter, a rules language, or any file format but this JSON.
- It does not check that the values are plausible. The schema's `meaning` says what a number is; whether 222 in is right for a goal post is for the project's own tests to say.
