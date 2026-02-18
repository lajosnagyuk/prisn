# Custom Runtimes

prisn can run scripts in any language. Built-in support includes Python, Node.js, Bash, Ruby, PHP, Perl, Lua, R, Julia, Deno, Bun, and Babashka.

For other languages, you can define custom runtimes.

## Quick Example

Add to `~/.prisn/runtimes.toml`:

```toml
[runtime.zig]
name = "Zig"
extensions = [".zig"]
command = "zig"
args = ["run", "{script}"]

[runtime.zig.detect]
check = "zig version"

[runtime.zig.install]
macos = "brew install zig"
linux = "snap install zig --classic"
```

Now you can:

```bash
prisn run hello.zig
```

## Runtime Definition Format

```toml
[runtime.ID]
# Required
name = "Human-Readable Name"
extensions = [".ext1", ".ext2"]
command = "interpreter"          # Command name or full path
args = ["{script}"]              # Use {script} as placeholder

# Optional
env = { "VAR" = "value" }        # Environment variables
shebang_patterns = ["^#!.*foo"]  # Regex patterns for shebang detection

# Detection (optional)
[runtime.ID.detect]
check = "command --version"      # Command to verify installation
version_regex = "v(\\d+\\.\\d+)" # Extract version from output
min_version = "1.0.0"            # Minimum required version

# Installation hints (optional)
[runtime.ID.install]
macos = "brew install foo"
linux = "apt-get install foo"
linux_amd64 = "curl -L ... | sh"
linux_arm64 = "curl -L ... | sh"
windows = "choco install foo"
script = "https://example.com/install.sh"
doc_url = "https://example.com/install"
note = "Manual installation required"

# Dependency management (optional)
[runtime.ID.deps]
enabled = true
manifest_files = ["deps.txt", "*.lock"]
parser = "requirements.txt"      # Built-in: requirements.txt, package.json, regex
install_command = "foo install {deps}"
self_managed = false             # true if runtime handles deps itself
env_type = "none"                # venv, node_modules, vendor, bundle, none
```

## Built-in Runtimes

### Python

```toml
[runtime.python3]
extensions = [".py", ".pyw"]
command = "python3"
args = ["-u", "{script}"]

[runtime.python3.deps]
enabled = true
manifest_files = ["requirements.txt", "pyproject.toml"]
parser = "requirements.txt"
env_type = "venv"
```

### Node.js

```toml
[runtime.node]
extensions = [".js", ".mjs", ".cjs"]
command = "node"
args = ["{script}"]

[runtime.node.deps]
enabled = true
manifest_files = ["package.json"]
parser = "package.json"
env_type = "node_modules"
```

### Babashka (Clojure)

```toml
[runtime.babashka]
extensions = [".bb", ".clj"]
command = "bb"
args = ["{script}"]

[runtime.babashka.deps]
enabled = true
manifest_files = ["bb.edn", "deps.edn"]
self_managed = true              # bb handles deps itself
env_type = "none"
```

## Examples

### Nushell

```toml
[runtime.nushell]
name = "Nushell"
extensions = [".nu"]
command = "nu"
args = ["{script}"]
shebang_patterns = ["^#!.*nu"]

[runtime.nushell.detect]
check = "nu --version"

[runtime.nushell.install]
macos = "brew install nushell"
linux = "cargo install nu"
```

### Fish Shell

```toml
[runtime.fish]
name = "Fish Shell"
extensions = [".fish"]
command = "fish"
args = ["{script}"]

[runtime.fish.detect]
check = "fish --version"

[runtime.fish.install]
macos = "brew install fish"
linux = "apt-get install fish"
```

### Zsh

```toml
[runtime.zsh]
name = "Zsh"
extensions = [".zsh"]
command = "zsh"
args = ["-e", "{script}"]

[runtime.zsh.deps]
enabled = false
```

### Elixir

```toml
[runtime.elixir]
name = "Elixir"
extensions = [".exs"]
command = "elixir"
args = ["{script}"]

[runtime.elixir.detect]
check = "elixir --version"
version_regex = "Elixir (\\d+\\.\\d+\\.\\d+)"

[runtime.elixir.install]
macos = "brew install elixir"
linux = "apt-get install elixir"

[runtime.elixir.deps]
enabled = true
manifest_files = ["mix.exs"]
install_command = "mix deps.get"
self_managed = true
```

### Racket

```toml
[runtime.racket]
name = "Racket"
extensions = [".rkt"]
command = "racket"
args = ["{script}"]

[runtime.racket.detect]
check = "racket --version"

[runtime.racket.install]
macos = "brew install racket"
linux = "apt-get install racket"

[runtime.racket.deps]
enabled = true
install_command = "raco pkg install {deps}"
self_managed = false
```

### Tcl

```toml
[runtime.tcl]
name = "Tcl"
extensions = [".tcl"]
command = "tclsh"
args = ["{script}"]

[runtime.tcl.detect]
check = "tclsh <<< 'puts [info patchlevel]'"
```

### AWK

```toml
[runtime.awk]
name = "AWK"
extensions = [".awk"]
command = "awk"
args = ["-f", "{script}"]

[runtime.awk.deps]
enabled = false
```

### Make (as a script runner)

```toml
[runtime.make]
name = "GNU Make"
extensions = [".mk", "Makefile"]
command = "make"
args = ["-f", "{script}"]

[runtime.make.deps]
enabled = false
```

### Crystal

```toml
[runtime.crystal]
name = "Crystal"
extensions = [".cr"]
command = "crystal"
args = ["run", "{script}"]

[runtime.crystal.detect]
check = "crystal --version"

[runtime.crystal.install]
macos = "brew install crystal"

[runtime.crystal.deps]
enabled = true
manifest_files = ["shard.yml"]
install_command = "shards install"
self_managed = true
```

### Nim

```toml
[runtime.nim]
name = "Nim"
extensions = [".nim"]
command = "nim"
args = ["r", "{script}"]

[runtime.nim.detect]
check = "nim --version"

[runtime.nim.install]
macos = "brew install nim"
script = "https://nim-lang.org/choosenim/init.sh"
```

### V

```toml
[runtime.v]
name = "V"
extensions = [".v"]
command = "v"
args = ["run", "{script}"]

[runtime.v.detect]
check = "v version"

[runtime.v.install]
script = "https://github.com/vlang/v/releases/latest/download/v_linux.zip"
```

### Haskell (runhaskell)

```toml
[runtime.haskell]
name = "Haskell"
extensions = [".hs"]
command = "runhaskell"
args = ["{script}"]

[runtime.haskell.detect]
check = "ghc --version"

[runtime.haskell.install]
script = "https://get-ghcup.haskell.org"
```

### OCaml

```toml
[runtime.ocaml]
name = "OCaml"
extensions = [".ml"]
command = "ocaml"
args = ["{script}"]

[runtime.ocaml.detect]
check = "ocaml --version"

[runtime.ocaml.install]
macos = "brew install ocaml"
linux = "apt-get install ocaml"
```

### Groovy

```toml
[runtime.groovy]
name = "Groovy"
extensions = [".groovy"]
command = "groovy"
args = ["{script}"]

[runtime.groovy.detect]
check = "groovy --version"

[runtime.groovy.install]
macos = "brew install groovy"
```

### Kotlin Script

```toml
[runtime.kotlin]
name = "Kotlin Script"
extensions = [".kts"]
command = "kotlin"
args = ["{script}"]

[runtime.kotlin.detect]
check = "kotlin -version"

[runtime.kotlin.install]
macos = "brew install kotlin"
```

### Scala (scala-cli)

```toml
[runtime.scala]
name = "Scala"
extensions = [".scala", ".sc"]
command = "scala-cli"
args = ["run", "{script}"]

[runtime.scala.detect]
check = "scala-cli version"

[runtime.scala.install]
script = "https://scala-cli.virtuslab.org/get"
```

## How It Works

1. **Extension Matching**: When you run `prisn run script.foo`, prisn looks for a runtime that handles `.foo` files.

2. **Shebang Detection**: If no extension match, prisn reads the first line and checks shebang patterns.

3. **Dependency Detection**: If `deps.enabled = true`, prisn checks for manifest files and parses dependencies.

4. **Environment Setup**: Based on `env_type`, prisn creates an isolated environment (venv, node_modules, etc.) or uses the system interpreter.

5. **Execution**: prisn runs `command args...` with the script path substituted for `{script}`.

## Tips

### Self-Managed Dependencies

For runtimes that handle their own dependency management (like Babashka, Deno, Julia), set:

```toml
[runtime.foo.deps]
enabled = true
self_managed = true
env_type = "none"
```

prisn won't try to install dependencies - the runtime does it automatically.

### No Dependencies

For simple runtimes with no package management:

```toml
[runtime.foo.deps]
enabled = false
```

### Custom Parsers

If your runtime uses a non-standard manifest format, you can use the regex parser:

```toml
[runtime.foo.deps]
parser = "regex"
# Parse lines like: name version
parser_regex = "^(\\S+)\\s+(\\S+)"
```

Or use a command to extract dependencies:

```toml
[runtime.foo.deps]
parser = "command"
parser_command = "foo deps list --json"
# Should output: {"deps": [{"name": "x", "version": "1.0"}]}
```

### Multiple Versions

You can define multiple versions of the same runtime:

```toml
[runtime.python39]
name = "Python 3.9"
extensions = []  # Don't auto-detect
command = "python3.9"
args = ["-u", "{script}"]

[runtime.python311]
name = "Python 3.11"
extensions = [".py"]  # Default for .py files
command = "python3.11"
args = ["-u", "{script}"]
```

Then use explicitly:

```bash
prisn run script.py --runtime python39
```
