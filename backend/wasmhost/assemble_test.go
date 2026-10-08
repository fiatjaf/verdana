package wasmhost

import (
	"os/exec"
	"strings"
)

// wat2wasm assembles WebAssembly text into the binary wazero will load. The
// tests need it to hold modules they build themselves rather than shipping
// binaries around; a machine without an assembler just skips those tests,
// which is why every call site treats a failure as a skip.
func wat2wasm(src, out string) error {
	assembler, err := exec.LookPath("wat2wasm")
	if err != nil {
		// wasm-tools reads the same text and is the other common way to
		// have an assembler lying around.
		if assembler, err = exec.LookPath("wasm-tools"); err != nil {
			return err
		}
		return runAssembler(assembler, src, out, "parse", src, "-o", out)
	}
	return runAssembler(assembler, src, out, src, "-o", out)
}

func runAssembler(exe, src, out string, args ...string) error {
	cmd := exec.Command(exe, args...)
	if msg, err := cmd.CombinedOutput(); err != nil {
		return &assemblerError{out: strings.TrimSpace(string(msg)), err: err}
	}
	return nil
}

type assemblerError struct {
	out string
	err error
}

func (e *assemblerError) Error() string {
	if e.out == "" {
		return e.err.Error()
	}
	return e.err.Error() + ": " + e.out
}
