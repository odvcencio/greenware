# Browser dependencies

The preview embeds the published distribution files. Node and npm are only
needed to refresh these files. Gzip uses `gzip -n -9` to omit the archive name
and timestamp.

| Package | Source file | SHA-256 before compression |
| --- | --- | --- |
| [@google/model-viewer 4.4.0](https://github.com/google/model-viewer/releases/tag/v4.4.0) | `dist/model-viewer.min.js` | `18b2e711035aa4705ee281530a55dc2338ff05eed3d70ae16d54105282a75ed5` |
| [htmx.org 2.0.11](https://htmx.org/docs/) | `dist/htmx.min.js` | `d6fdc75f204e6bdefa99b69bf1e6d4ac69b8a364f77929f45c13476b4000f717` |

Fetch each pinned archive with `npm pack --ignore-scripts <package>@<version>`.
Extract the named file and the package's `LICENSE`. Both scripts are served by
the local Go command.
