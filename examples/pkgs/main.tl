--- Package demo: `tlua mod fetch` + `tlua mod vendor`, then build.
--- require maps dotted paths under tlua_modules (see SPEC §11).
local G = require("example.com.greet.greet") as { hello(name: string): string }
print(G.hello("tlua"))
