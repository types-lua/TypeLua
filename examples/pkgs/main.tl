--- Package demo with direct + indirect dependencies (all local, no network):
---   tlua mod fetch    # resolve -> tlua.lock (words lands as indirect)
---   tlua mod vendor   # copy sources -> tlua_modules/
---   tlua check && tlua build
--- Imports infer module types from source (no annotations needed);
--- require maps dotted paths under tlua_modules (see SPEC §11).
--- `tlua mod list` shows greet + notes; `tlua mod list --indirect` adds words.
import G from "example.com.greet.greet"
import N from "example.com.notes.notes"
print(G.hello("tlua"))
print(N.title("hello world"))
