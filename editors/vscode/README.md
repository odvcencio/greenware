# Greenware for VS Code

Provides syntax highlighting, bracket matching, and line comments for Greenware files. The extension is declarative and does not need a language server.

To try it from the repository, run `code --extensionDevelopmentPath=/absolute/path/to/greenware/editors/vscode /absolute/path/to/greenware`. The extension development window loads the grammar. To install it in other windows, package this folder with `vsce package` and use `code --install-extension` on the resulting VSIX.

With `greenware` on the editor terminal's PATH, open a `.gw` asset and run **Greenware: Build active asset**, **Greenware: Watch active asset**, or **Greenware: View active asset** from **Tasks: Run Task**. The tasks are available when this repository is open as a workspace. The build task links diagnostics to the source. The watch task writes preview GLBs to `out/preview` and can be stopped from the terminal.

[VS Code syntax highlighting guide](https://code.visualstudio.com/api/language-extensions/syntax-highlight-guide)
