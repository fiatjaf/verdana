package webview

import (
	"os/exec"
	"strings"
	"testing"
)

func TestShimProvidesShellCapabilityDiscovery(t *testing.T) {
	for _, want := range []string{
		`const napplet = { shell: createShellGlobal() };`,
		`["shell.", handleShellMessage]`,
		`supports(domain)`,
	} {
		if !strings.Contains(shimPrelude, want) {
			t.Fatalf("shim is missing %q", want)
		}
	}

	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed; static shell API checks passed")
	}
	script := shimPrelude + `
globalThis.window = globalThis;
window.parent = {};
const listeners = {};
window.addEventListener = (type, handler) => { listeners[type] = handler; };
const napplet = NappletShimPrelude.install({domains:["theme"]});
if (!napplet.shell || napplet.shell.supports("theme")) throw new Error("bad pre-init state");
if (!napplet.theme || !napplet.theme.get) throw new Error("theme API missing");
let observed = null;
napplet.shell.onReady(env => { observed = env; });
const ready = napplet.shell.ready();
listeners.message({source:window.parent,data:{type:"shell.init",capabilities:{domains:["theme"]},services:["proxy"]}});
ready.then(env => {
  if (!napplet.shell.supports("theme") || napplet.shell.supports("unknown")) throw new Error("supports is wrong");
  if (napplet.shell.services[0] !== "proxy" || observed !== env) throw new Error("environment not delivered");
}).catch(err => { console.error(err); process.exitCode = 1; });
`
	cmd := exec.Command(node, "-")
	cmd.Stdin = strings.NewReader(script)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("shell shim failed: %v\n%s", err, out)
	}
}
