--- Package demo with direct + indirect dependencies (all local, no network):
---   tlua mod fetch    # resolve -> tlua.lock (words lands as indirect)
---   tlua mod vendor   # copy sources -> tlua_modules/
---   tlua check && tlua build
--- require maps dotted paths under tlua_modules (see SPEC §11).
--- `tlua mod list` shows greet + notes; `tlua mod list --indirect` adds words.
import G: { hello(name: string): string } from "example.com.greet.greet"
import N: { title(s: string): string } from "example.com.notes.notes"
print(G.hello("tlua"))
print(N.title("hello world"))
