--- Package demo with direct + indirect dependencies (all local, no network):
---   tlua mod fetch    # resolve -> tlua.lock (words lands as indirect)
---   tlua mod vendor   # copy sources -> tlua_modules/
---   tlua check && tlua build
--- require maps dotted paths under tlua_modules (see SPEC §11).
--- `tlua mod list` shows greet + notes; `tlua mod list --indirect` adds words.
local G = require("example.com.greet.greet") as { hello(name: string): string }
local N = require("example.com.notes.notes") as { title(s: string): string }
print(G.hello("tlua"))
print(N.title("hello world"))
