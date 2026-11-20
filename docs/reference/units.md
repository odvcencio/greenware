# Units in number literals

A number literal may carry a unit suffix with no space between the digits and the suffix. A literal without a suffix is pending: it takes the asset's default unit from the slot it fills (`language.md` section 2.4).

| Dimension | Suffixes | Value |
|---|---|---|
| Length | `mm`, `cm`, `m`, `km` | metric, exact |
| Length | `ft`, `yd` | 0.3048 m, 0.9144 m exactly |
| Length | `in` | 0.0254 m exactly (decision 0019) |
| Angle | `deg`, `rad` | `deg` is the literal times the binary64 constant for pi/180 |
| Duration | `s`, `ms` | |
| Pixels | `px` | |
| Fraction | `%` | the number divided by 100 |
| Bytes | `B`, `KiB`, `MiB`, `GiB` | 1, 1024, 1048576, 1073741824 |

A decimal with a rational unit factor is converted exactly and rounded once to binary64, ties to even. So `1in` is the same number as `25.4mm`, `12in` as `1ft` and `36in` as `1yd`.

## The inch

`in` is also the range word, so the rule depends on a space.

| Written | Meaning |
|---|---|
| `4in`, `18.5in`, `6in` | an inch literal |
| `param p : length = 4in in 2in..6in` | default 4 in, range 2 in to 6 in |
| `param p : length = 4m in 2m..5m` | default 4 m, range 2 m to 5 m: the range word |
| `sample a in 0deg..90deg by 9` | the range word |
| `4 in` (a space) in a length slot | a syntax error: `write the inch unit without a space: 4in` |

`in` is a suffix of a literal only. `asset { length = in }` is not accepted: the asset's default length unit is one of `m`, `mm`, `cm`, `km`, `yd`, `ft`. There is no `turn` unit.
