--- Package demo with direct + indirect dependencies (all local, no network):
---   tlua mod fetch    # resolve -> tlua.lock (words lands as indirect)
---   tlua mod vendor   # copy sources -> tlua_modules/
---   tlua check && tlua build
--- Imports name packages, not files (`init.tl` is the package root);
--- a manifest short name (`greet`) works too when unambiguous.
--- require maps dotted paths under tlua_modules (see SPEC §11).
--- `tlua mod list` shows greet + notes; `tlua mod list --indirect` adds words.
import G from "example.com.greet"
import N from "example.com.notes"
print(G.hello("tlua"))
print(N.title("hello world"))
