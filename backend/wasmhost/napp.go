package wasmhost

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"runtime"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"

	"verdana/backend/wasm"
)

// Napp is one wasm napp running inside this process: the module wazero
// instantiated, the functions it exports, and the canvas it last painted.
//
// The module gets the canvas imports and napp_call, and nothing else. There
// is no WASI here, and no way to add one: a napp that wants a file, a socket
// or a clock of its own asks the launcher for it through napp_call like every
// other napp does, so the capability stays somewhere permission can see it.
type Napp struct {
	rt    wazero.Runtime
	mod   api.Module
	frame api.Function
	ptr   api.Function
	event api.Function

	// alloc and receive are how messages get into the module: present
	// whenever it imports napp_call, since its answers come back this way.
	alloc   api.Function
	receive api.Function

	// call is napp_call's body: the bridge, or nil when there is none.
	call func(method string, params []byte) int32

	// width and height are what the canvas is right now. The imports read
	// them, so a napp learns about a resize by asking.
	width, height int32

	// started is the window's clock origin, so napp_now_ms counts from the
	// window opening the way a napp's own time.Time would.
	started float64

	// img is the canvas as Go sees it, copied out of the module's memory so
	// it can become a texture. Allocated at the canvas size and refilled.
	img []byte

	animating bool
}

// Load compiles and instantiates the module at path. call is what the
// module's napp_call reaches; nil answers every call with id 0.
func Load(path string, nowMS func() float64, call func(method string, params []byte) int32) (*Napp, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	ctx := context.Background()
	rt := wazero.NewRuntimeWithConfig(ctx, runtimeConfig())
	// An empty start-function list is the whole point: a napp has no _start
	// to run, the launcher drives it a frame at a time.
	cfg := wazero.NewModuleConfig().WithName("").WithStartFunctions()

	n := &Napp{started: nowMS(), call: call}

	if _, err := rt.NewHostModuleBuilder(wasm.HostModule).
		NewFunctionBuilder().
		WithFunc(func() int32 { return n.width }).
		Export(wasm.ImportCanvasWidth).
		NewFunctionBuilder().
		WithFunc(func() int32 { return n.height }).
		Export(wasm.ImportCanvasHeight).
		NewFunctionBuilder().
		WithFunc(func() float64 { return nowMS() - n.started }).
		Export(wasm.ImportNowMs).
		NewFunctionBuilder().
		WithFunc(func(offset, length uint32) { n.say(int32(offset), int32(length)) }).
		Export(wasm.ImportLog).
		NewFunctionBuilder().
		WithFunc(func(ctx context.Context, m api.Module, mPtr, mLen, pPtr, pLen uint32) int32 {
			return n.hostCall(m, mPtr, mLen, pPtr, pLen)
		}).
		Export(wasm.ImportCall).
		Instantiate(ctx); err != nil {
		rt.Close(ctx)
		return nil, err
	}

	// Compiling is the slow part of opening a wasm napp, so it happens here
	// rather than inside the first frame.
	compiled, err := rt.CompileModule(ctx, raw)
	if err != nil {
		rt.Close(ctx)
		return nil, fmt.Errorf("this is not a wasm module: %w", err)
	}

	// A module that imports more than the canvas is refused here, with what
	// it asked for in the message: the most likely reason is a build that
	// expects WASI, and that never works here.
	if absent := missingImports(compiled); len(absent) > 0 {
		rt.Close(ctx)
		return nil, fmt.Errorf(
			"a wasm napp gets a canvas and nothing else, but this module also imports %v "+
				"(it must be built with no WASI and no other imports)", absent)
	}

	mod, err := rt.InstantiateModule(ctx, compiled, cfg)
	if err != nil {
		rt.Close(ctx)
		return nil, fmt.Errorf("could not start this wasm module: %w", err)
	}
	n.mod = mod
	n.rt = rt

	for _, name := range []string{wasm.ExportFrame, wasm.ExportCanvasPtr} {
		if mod.ExportedFunction(name) == nil {
			mod.Close(ctx)
			rt.Close(ctx)
			return nil, fmt.Errorf("the module does not export %s", name)
		}
	}
	n.frame = mod.ExportedFunction(wasm.ExportFrame)
	n.ptr = mod.ExportedFunction(wasm.ExportCanvasPtr)
	// Input is the one export that may be absent: a napp that only draws is
	// still perfectly valid.
	n.event = mod.ExportedFunction(wasm.ExportEvent)

	// A napp that calls out must have somewhere for the answers to land.
	// One that does not call is spared the two exports.
	n.alloc = mod.ExportedFunction(wasm.ExportAlloc)
	n.receive = mod.ExportedFunction(wasm.ExportReceive)
	if importsCall(compiled) && (n.alloc == nil || n.receive == nil) {
		mod.Close(ctx)
		rt.Close(ctx)
		return nil, fmt.Errorf("the module imports %s but does not export both %s and %s, "+
			"so there is no way to hand it the answers", wasm.ImportCall, wasm.ExportAlloc, wasm.ExportReceive)
	}

	log.Info().Str("wasm", path).Bool("input", n.event != nil).Bool("calls", n.receive != nil).
		Msg("wasm napp ready")
	return n, nil
}

// runtimeConfig is wazero's default (its compiler where it supports the
// platform, its interpreter elsewhere), except on Android: wazero leaves
// GOOS=android off the compiler's list, though Android is a Linux kernel and
// the compiler's code is built for it, and the interpreter is two orders of
// magnitude slower — a frame of an egui napp takes over half a second. So on
// the 64-bit phones the compiler is asked for by name.
func runtimeConfig() wazero.RuntimeConfig {
	cfg := wazero.NewRuntimeConfig()
	if runtime.GOOS == "android" && (runtime.GOARCH == "arm64" || runtime.GOARCH == "amd64") {
		cfg = wazero.NewRuntimeConfigCompiler()
	}
	if compileCache != nil {
		cfg = cfg.WithCompilationCache(compileCache)
	}
	// No WithCloseOnContextDone, though it would let Close stop a module
	// stuck in a loop: it makes an egui napp's frames six times slower.
	return cfg
}

// compileCache is where compiled modules are kept between windows, if anywhere.
var compileCache wazero.CompilationCache

// SetCacheDir keeps what the compiler makes of a module in dir, so a napp is
// compiled the first time it opens and not again: a phone takes seconds to
// compile an egui napp, which is seconds of a blank window every time. The
// cache is keyed on the module's bytes, so an update is compiled afresh. Call
// it before the first Load; one process at a time per dir.
func SetCacheDir(dir string) error {
	c, err := wazero.NewCompilationCacheWithDir(dir)
	if err != nil {
		return err
	}
	compileCache = c
	return nil
}

// missingImports is everything the module imports as functions that a host
// does not provide, as "module.name" strings.
//
// It goes through FunctionDefinition.Import rather than ModuleName/Name: those
// report the module's optional name section, which is empty in almost every
// wasm file, so they answer "" for everything. Function imports only — a
// module importing a table, a memory or a global fails to instantiate with
// wazero's own message, which is clear enough on its own.
func missingImports(compiled wazero.CompiledModule) []string {
	imports := compiled.ImportedFunctions()
	list := make([]wasm.Import, 0, len(imports))
	for _, f := range imports {
		module, name, isImport := f.Import()
		if !isImport {
			continue
		}
		list = append(list, wasm.Import{Module: module, Name: name})
	}
	return wasm.MissingImports(list)
}

// importsCall says whether the module imports napp_call.
func importsCall(compiled wazero.CompiledModule) bool {
	for _, f := range compiled.ImportedFunctions() {
		if module, name, ok := f.Import(); ok && module == wasm.HostModule && name == wasm.ImportCall {
			return true
		}
	}
	return false
}

// Resize tells the module the canvas it should paint at. The next frame sees
// it.
func (n *Napp) Resize(width, height int) {
	n.width, n.height = int32(width), int32(height)
}

// Frame advances the module by dt milliseconds and reads the canvas back out
// of its memory. Pixels() is then valid.
//
// The canvas is read after every frame, whatever napp_frame returned: its
// result only says whether the napp wants another frame without being
// poked, not whether this one painted. A napp that settles on its very first
// frame (which is most of them) has still painted that frame.
func (n *Napp) Frame(dt float64) error {
	res, err := n.frame.Call(context.Background(), math.Float64bits(dt))
	if err != nil {
		return err
	}
	// A napp that keeps asking for frames is animating; the window keeps
	// rendering until it stops, which is also what lets an animation run
	// with nothing happening.
	n.animating = len(res) > 0 && res[0] != 0
	return n.readCanvas()
}

// readCanvas copies width*height*4 bytes from wherever the module says its
// canvas is. The module is free to move it (a new allocation, a bigger one
// after a resize), so this is a fresh copy every time.
func (n *Napp) readCanvas() error {
	res, err := n.ptr.Call(context.Background())
	if err != nil {
		return err
	}
	if len(res) == 0 {
		return errors.New("napp_canvas_ptr returned nothing")
	}

	mem := n.mod.Memory()
	off := uint32(res[0])
	size := uint32(wasm.CanvasBytes(int(n.width), int(n.height)))
	if uint64(off)+uint64(size) > uint64(mem.Size()) {
		return fmt.Errorf("the canvas at %d+%d does not fit in %d bytes of wasm memory",
			off, size, mem.Size())
	}
	src, ok := mem.Read(off, size)
	if !ok {
		return fmt.Errorf("could not read the canvas at %d", off)
	}

	if cap(n.img) < int(size) {
		n.img = make([]byte, size)
	}
	n.img = n.img[:size]
	copy(n.img, src)
	return nil
}

// Pixels is the canvas as RGBA8888, valid after a Frame.
func (n *Napp) Pixels() []byte { return n.img }

// HasCanvas says whether there is anything to show yet.
func (n *Napp) HasCanvas() bool { return len(n.img) > 0 }

// Width and Height are the canvas size the module was last told about.
func (n *Napp) Width() int  { return int(n.width) }
func (n *Napp) Height() int { return int(n.height) }

// Animating says whether the module asked for another frame without being
// poked.
func (n *Napp) Animating() bool { return n.animating }

// Input hands the module one event, if it takes input.
func (n *Napp) Input(kind int32, x, y int32, buttons int32, a, b float32) {
	if n.event == nil {
		return
	}
	if _, err := n.event.Call(context.Background(),
		uint64(uint32(kind)), uint64(uint32(x)), uint64(uint32(y)), uint64(uint32(buttons)),
		uint64(math.Float32bits(a)), uint64(math.Float32bits(b)),
	); err != nil {
		log.Warn().Err(err).Int("kind", int(kind)).Msg("napp rejected an input event")
	}
}

// hostCall is the body of the napp_call import: read the method and params
// out of the module's memory and hand them to the bridge, which answers
// later. Never blocks, never calls back into the module.
func (n *Napp) hostCall(m api.Module, mPtr, mLen, pPtr, pLen uint32) int32 {
	if n.call == nil || mLen == 0 || mLen > 256 || pLen > maxMessage {
		return 0
	}
	mem := m.Memory()
	method, ok := mem.Read(mPtr, mLen)
	if !ok {
		return 0
	}
	params, ok := mem.Read(pPtr, pLen)
	if !ok {
		return 0
	}
	// both are views into the module's memory, which the module is free
	// to reuse as soon as this returns
	return n.call(string(method), append([]byte(nil), params...))
}

// maxMessage caps what a napp may hand napp_call in one go, the size of a
// saveFile payload being the practical limit.
const maxMessage = 64 << 20

// Deliver hands the module one message through napp_receive: the bytes go
// into room it allocates with napp_alloc, which it then owns. A module with
// no napp_receive called nothing and has nothing coming.
func (n *Napp) Deliver(kind, id int32, data []byte) error {
	if n.receive == nil {
		return nil
	}
	ctx := context.Background()
	var ptr uint32
	if len(data) > 0 {
		res, err := n.alloc.Call(ctx, uint64(uint32(len(data))))
		if err != nil {
			return err
		}
		if len(res) == 0 {
			return errors.New("napp_alloc returned nothing")
		}
		ptr = uint32(res[0])
		// the memory may have grown inside napp_alloc, so it is looked up
		// only now
		if !n.mod.Memory().Write(ptr, data) {
			return fmt.Errorf("napp_alloc gave %d, which does not fit %d bytes", ptr, len(data))
		}
	}
	_, err := n.receive.Call(ctx, uint64(uint32(kind)), uint64(uint32(id)), uint64(ptr), uint64(uint32(len(data))))
	return err
}

// say passes one of the module's log lines on to the launcher's own log.
// It is the body of the napp_log import, so it runs on the module's thread
// and must not do anything slow.
func (n *Napp) say(offset, length int32) {
	if length <= 0 || length > 64<<10 {
		return
	}
	mem := n.mod.Memory()
	if uint64(offset)+uint64(length) > uint64(mem.Size()) {
		return
	}
	b, ok := mem.Read(uint32(offset), uint32(length))
	if !ok {
		return
	}
	log.Info().Str("napp", string(b)).Msg("wasm napp")
}

// Close gives the module and the runtime back. Not while the module is
// running: the runtime's compiled code goes with it.
func (n *Napp) Close() {
	ctx := context.Background()
	if n.mod != nil {
		n.mod.Close(ctx)
	}
	if n.rt != nil {
		n.rt.Close(ctx)
	}
}
