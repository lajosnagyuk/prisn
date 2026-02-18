package runtime

// BuiltinRuntimes returns the default runtime definitions.
func BuiltinRuntimes() []*Runtime {
	return []*Runtime{
		pythonRuntime(),
		nodeRuntime(),
		bashRuntime(),
		babashkaRuntime(),
		perlRuntime(),
		phpRuntime(),
		rubyRuntime(),
		luaRuntime(),
		rRuntime(),
		juliaRuntime(),
		denoRuntime(),
		bunRuntime(),
	}
}

func pythonRuntime() *Runtime {
	return &Runtime{
		ID:         "python3",
		Name:       "Python 3",
		Extensions: []string{".py", ".pyw"},
		Command:    "python3",
		Args:       []string{"-u", "{script}"}, // -u: unbuffered output
		ShebangPatterns: []string{
			`^#!.*python3?`,
			`^#!.*env\s+python`,
		},
		Builtin: true,
		Detect: DetectConfig{
			Check:        "python3 --version",
			VersionRegex: `Python (\d+\.\d+\.\d+)`,
			MinVersion:   "3.8",
		},
		Install: InstallConfig{
			Commands: map[string]string{
				"macos":       "brew install python@3.11",
				"linux":       "apt-get install python3 python3-venv python3-pip",
				"linux_amd64": "apt-get install python3 python3-venv python3-pip",
			},
			DocURL: "https://www.python.org/downloads/",
		},
		Deps: DepsConfig{
			Enabled:       true,
			ManifestFiles: []string{"requirements.txt", "pyproject.toml", "setup.py"},
			Parser:        "requirements.txt",
			InstallCommand: "pip install -r {manifest}",
			EnvType:       "venv",
			StdlibModules: pythonStdlibList(),
			ImportToPackage: map[string]string{
				"cv2":        "opencv-python",
				"PIL":        "pillow",
				"sklearn":    "scikit-learn",
				"skimage":    "scikit-image",
				"yaml":       "pyyaml",
				"bs4":        "beautifulsoup4",
				"dotenv":     "python-dotenv",
				"dateutil":   "python-dateutil",
				"jwt":        "pyjwt",
				"serial":     "pyserial",
				"usb":        "pyusb",
				"git":        "gitpython",
				"psycopg2":   "psycopg2-binary",
				"torch":      "torch",
				"tf":         "tensorflow",
				"np":         "numpy",
				"pd":         "pandas",
				"plt":        "matplotlib",
				"sns":        "seaborn",
			},
		},
	}
}

func nodeRuntime() *Runtime {
	return &Runtime{
		ID:         "node",
		Name:       "Node.js",
		Extensions: []string{".js", ".mjs", ".cjs"},
		Command:    "node",
		Args:       []string{"{script}"},
		ShebangPatterns: []string{
			`^#!.*node`,
			`^#!.*env\s+node`,
		},
		Builtin: true,
		Detect: DetectConfig{
			Check:        "node --version",
			VersionRegex: `v(\d+\.\d+\.\d+)`,
			MinVersion:   "18.0.0",
		},
		Install: InstallConfig{
			Commands: map[string]string{
				"macos": "brew install node",
				"linux": "curl -fsSL https://deb.nodesource.com/setup_20.x | sudo -E bash - && sudo apt-get install -y nodejs",
			},
			DocURL: "https://nodejs.org/",
		},
		Deps: DepsConfig{
			Enabled:        true,
			ManifestFiles:  []string{"package.json"},
			Parser:         "package.json",
			InstallCommand: "npm install",
			EnvType:        "node_modules",
			StdlibModules: []string{
				"fs", "path", "http", "https", "url", "util", "os", "crypto",
				"stream", "events", "buffer", "child_process", "cluster", "dgram",
				"dns", "net", "readline", "repl", "tls", "tty", "v8", "vm", "zlib",
				"assert", "async_hooks", "console", "constants", "domain", "inspector",
				"module", "perf_hooks", "process", "punycode", "querystring",
				"string_decoder", "sys", "timers", "trace_events", "worker_threads",
			},
		},
	}
}

func bashRuntime() *Runtime {
	return &Runtime{
		ID:         "bash",
		Name:       "Bash",
		Extensions: []string{".sh", ".bash"},
		Command:    "bash",
		Args:       []string{"-e", "{script}"}, // -e: exit on error
		ShebangPatterns: []string{
			`^#!.*bash`,
			`^#!/bin/sh`,
			`^#!.*env\s+bash`,
		},
		Builtin: true,
		Detect: DetectConfig{
			Check: "bash --version",
		},
		Install: InstallConfig{
			Note: "Bash is pre-installed on most Unix systems",
		},
		Deps: DepsConfig{
			Enabled: false, // Bash has no package manager
		},
	}
}

func babashkaRuntime() *Runtime {
	return &Runtime{
		ID:         "babashka",
		Name:       "Babashka",
		Extensions: []string{".bb", ".clj"},
		Command:    "bb",
		Args:       []string{"{script}"},
		ShebangPatterns: []string{
			`^#!.*bb`,
			`^#!.*babashka`,
			`^#!.*env\s+bb`,
		},
		Builtin: true,
		Detect: DetectConfig{
			Check:        "bb --version",
			VersionRegex: `babashka v?(\d+\.\d+\.\d+)`,
		},
		Install: InstallConfig{
			Script: "https://raw.githubusercontent.com/babashka/babashka/master/install",
			Commands: map[string]string{
				"macos":       "brew install borkdude/brew/babashka",
				"linux_amd64": "curl -sLO https://raw.githubusercontent.com/babashka/babashka/master/install && chmod +x install && ./install",
			},
			DocURL: "https://babashka.org/",
		},
		Deps: DepsConfig{
			Enabled:       true,
			ManifestFiles: []string{"bb.edn", "deps.edn"},
			SelfManaged:   true, // Babashka handles deps itself via bb.edn
			EnvType:       "none",
		},
	}
}

func perlRuntime() *Runtime {
	return &Runtime{
		ID:         "perl",
		Name:       "Perl",
		Extensions: []string{".pl", ".pm"},
		Command:    "perl",
		Args:       []string{"{script}"},
		ShebangPatterns: []string{
			`^#!.*perl`,
			`^#!.*env\s+perl`,
		},
		Builtin: true,
		Detect: DetectConfig{
			Check:        "perl --version",
			VersionRegex: `v(\d+\.\d+\.\d+)`,
		},
		Install: InstallConfig{
			Commands: map[string]string{
				"macos": "brew install perl",
				"linux": "apt-get install perl",
			},
			DocURL: "https://www.perl.org/",
		},
		Deps: DepsConfig{
			Enabled:        true,
			ManifestFiles:  []string{"cpanfile", "Makefile.PL"},
			InstallCommand: "cpanm --installdeps .",
			EnvType:        "local_lib",
		},
	}
}

func phpRuntime() *Runtime {
	return &Runtime{
		ID:         "php",
		Name:       "PHP",
		Extensions: []string{".php"},
		Command:    "php",
		Args:       []string{"{script}"},
		ShebangPatterns: []string{
			`^#!.*php`,
			`^#!.*env\s+php`,
		},
		Builtin: true,
		Detect: DetectConfig{
			Check:        "php --version",
			VersionRegex: `PHP (\d+\.\d+\.\d+)`,
			MinVersion:   "8.0",
		},
		Install: InstallConfig{
			Commands: map[string]string{
				"macos": "brew install php",
				"linux": "apt-get install php-cli",
			},
			DocURL: "https://www.php.net/",
		},
		Deps: DepsConfig{
			Enabled:        true,
			ManifestFiles:  []string{"composer.json"},
			InstallCommand: "composer install",
			EnvType:        "vendor",
		},
	}
}

func rubyRuntime() *Runtime {
	return &Runtime{
		ID:         "ruby",
		Name:       "Ruby",
		Extensions: []string{".rb"},
		Command:    "ruby",
		Args:       []string{"{script}"},
		ShebangPatterns: []string{
			`^#!.*ruby`,
			`^#!.*env\s+ruby`,
		},
		Builtin: true,
		Detect: DetectConfig{
			Check:        "ruby --version",
			VersionRegex: `ruby (\d+\.\d+\.\d+)`,
		},
		Install: InstallConfig{
			Commands: map[string]string{
				"macos": "brew install ruby",
				"linux": "apt-get install ruby",
			},
			DocURL: "https://www.ruby-lang.org/",
		},
		Deps: DepsConfig{
			Enabled:        true,
			ManifestFiles:  []string{"Gemfile"},
			InstallCommand: "bundle install",
			EnvType:        "bundle",
		},
	}
}

func luaRuntime() *Runtime {
	return &Runtime{
		ID:         "lua",
		Name:       "Lua",
		Extensions: []string{".lua"},
		Command:    "lua",
		Args:       []string{"{script}"},
		ShebangPatterns: []string{
			`^#!.*lua`,
			`^#!.*env\s+lua`,
		},
		Builtin: true,
		Detect: DetectConfig{
			Check:        "lua -v",
			VersionRegex: `Lua (\d+\.\d+)`,
		},
		Install: InstallConfig{
			Commands: map[string]string{
				"macos": "brew install lua",
				"linux": "apt-get install lua5.4",
			},
		},
		Deps: DepsConfig{
			Enabled:        true,
			ManifestFiles:  []string{"*.rockspec"},
			InstallCommand: "luarocks install --tree=lua_modules --only-deps {manifest}",
			EnvType:        "lua_modules",
		},
	}
}

func rRuntime() *Runtime {
	return &Runtime{
		ID:         "r",
		Name:       "R",
		Extensions: []string{".r", ".R"},
		Command:    "Rscript",
		Args:       []string{"{script}"},
		ShebangPatterns: []string{
			`^#!.*Rscript`,
			`^#!.*env\s+Rscript`,
		},
		Builtin: true,
		Detect: DetectConfig{
			Check:        "Rscript --version",
			VersionRegex: `R scripting front-end version (\d+\.\d+\.\d+)`,
		},
		Install: InstallConfig{
			Commands: map[string]string{
				"macos": "brew install r",
				"linux": "apt-get install r-base",
			},
			DocURL: "https://www.r-project.org/",
		},
		Deps: DepsConfig{
			Enabled:        true,
			ManifestFiles:  []string{"DESCRIPTION", "renv.lock"},
			InstallCommand: "Rscript -e 'renv::restore()'",
			EnvType:        "renv",
		},
	}
}

func juliaRuntime() *Runtime {
	return &Runtime{
		ID:         "julia",
		Name:       "Julia",
		Extensions: []string{".jl"},
		Command:    "julia",
		Args:       []string{"{script}"},
		ShebangPatterns: []string{
			`^#!.*julia`,
			`^#!.*env\s+julia`,
		},
		Builtin: true,
		Detect: DetectConfig{
			Check:        "julia --version",
			VersionRegex: `julia version (\d+\.\d+\.\d+)`,
		},
		Install: InstallConfig{
			Commands: map[string]string{
				"macos": "brew install julia",
				"linux": "curl -fsSL https://install.julialang.org | sh",
			},
			DocURL: "https://julialang.org/downloads/",
		},
		Deps: DepsConfig{
			Enabled:        true,
			ManifestFiles:  []string{"Project.toml"},
			InstallCommand: "julia -e 'using Pkg; Pkg.instantiate()'",
			SelfManaged:    true, // Julia manages deps internally
			EnvType:        "none",
		},
	}
}

func denoRuntime() *Runtime {
	return &Runtime{
		ID:         "deno",
		Name:       "Deno",
		Extensions: []string{".ts", ".tsx"},
		Command:    "deno",
		Args:       []string{"run", "--allow-all", "{script}"},
		ShebangPatterns: []string{
			`^#!.*deno`,
			`^#!.*env\s+deno`,
		},
		Builtin: true,
		Detect: DetectConfig{
			Check:        "deno --version",
			VersionRegex: `deno (\d+\.\d+\.\d+)`,
		},
		Install: InstallConfig{
			Script: "https://deno.land/install.sh",
			Commands: map[string]string{
				"macos": "brew install deno",
				"linux": "curl -fsSL https://deno.land/install.sh | sh",
			},
			DocURL: "https://deno.land/",
		},
		Deps: DepsConfig{
			Enabled:     true,
			SelfManaged: true, // Deno downloads deps on-demand from URLs
			EnvType:     "none",
		},
	}
}

func bunRuntime() *Runtime {
	return &Runtime{
		ID:         "bun",
		Name:       "Bun",
		Extensions: []string{}, // Uses same extensions as Node, but detected by shebang
		Command:    "bun",
		Args:       []string{"run", "{script}"},
		ShebangPatterns: []string{
			`^#!.*bun`,
			`^#!.*env\s+bun`,
		},
		Builtin: true,
		Detect: DetectConfig{
			Check:        "bun --version",
			VersionRegex: `(\d+\.\d+\.\d+)`,
		},
		Install: InstallConfig{
			Script: "https://bun.sh/install",
			Commands: map[string]string{
				"macos": "brew install oven-sh/bun/bun",
				"linux": "curl -fsSL https://bun.sh/install | bash",
			},
			DocURL: "https://bun.sh/",
		},
		Deps: DepsConfig{
			Enabled:        true,
			ManifestFiles:  []string{"package.json", "bun.lockb"},
			InstallCommand: "bun install",
			EnvType:        "node_modules",
		},
	}
}

// pythonStdlibList returns Python standard library module names.
func pythonStdlibList() []string {
	return []string{
		"abc", "aifc", "argparse", "array", "ast", "asynchat", "asyncio",
		"asyncore", "atexit", "audioop", "base64", "bdb", "binascii",
		"binhex", "bisect", "builtins", "bz2", "calendar", "cgi", "cgitb",
		"chunk", "cmath", "cmd", "code", "codecs", "codeop", "collections",
		"colorsys", "compileall", "concurrent", "configparser", "contextlib",
		"contextvars", "copy", "copyreg", "cProfile", "crypt", "csv",
		"ctypes", "curses", "dataclasses", "datetime", "dbm", "decimal",
		"difflib", "dis", "distutils", "doctest", "email", "encodings",
		"enum", "errno", "faulthandler", "fcntl", "filecmp", "fileinput",
		"fnmatch", "fractions", "ftplib", "functools", "gc", "getopt",
		"getpass", "gettext", "glob", "graphlib", "grp", "gzip", "hashlib",
		"heapq", "hmac", "html", "http", "idlelib", "imaplib", "imghdr",
		"imp", "importlib", "inspect", "io", "ipaddress", "itertools",
		"json", "keyword", "lib2to3", "linecache", "locale", "logging",
		"lzma", "mailbox", "mailcap", "marshal", "math", "mimetypes",
		"mmap", "modulefinder", "multiprocessing", "netrc", "nis",
		"nntplib", "numbers", "operator", "optparse", "os", "ossaudiodev",
		"pathlib", "pdb", "pickle", "pickletools", "pipes", "pkgutil",
		"platform", "plistlib", "poplib", "posix", "posixpath", "pprint",
		"profile", "pstats", "pty", "pwd", "py_compile", "pyclbr",
		"pydoc", "queue", "quopri", "random", "re", "readline", "reprlib",
		"resource", "rlcompleter", "runpy", "sched", "secrets", "select",
		"selectors", "shelve", "shlex", "shutil", "signal", "site",
		"smtpd", "smtplib", "sndhdr", "socket", "socketserver", "spwd",
		"sqlite3", "ssl", "stat", "statistics", "string", "stringprep",
		"struct", "subprocess", "sunau", "symtable", "sys", "sysconfig",
		"syslog", "tabnanny", "tarfile", "telnetlib", "tempfile", "termios",
		"test", "textwrap", "threading", "time", "timeit", "tkinter",
		"token", "tokenize", "tomllib", "trace", "traceback", "tracemalloc",
		"tty", "turtle", "turtledemo", "types", "typing", "unicodedata",
		"unittest", "urllib", "uu", "uuid", "venv", "warnings", "wave",
		"weakref", "webbrowser", "winreg", "winsound", "wsgiref", "xdrlib",
		"xml", "xmlrpc", "zipapp", "zipfile", "zipimport", "zlib",
		"__future__", "_thread",
	}
}
