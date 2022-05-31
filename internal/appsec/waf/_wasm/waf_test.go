// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package wasm

import (
	"bytes"
	"context"
	"embed"
	"fmt"
	"os"
	"strings"
	"testing"
	"text/template"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/wasi"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/waf"
)

//go:embed libddwaf.wasm
var fs embed.FS

type vm struct {
	wazero.Runtime
	memory       api.Memory
	malloc_      api.Function
	free_        api.Function
	enableLogger api.Function
	encode_      api.Function
	init_        api.Function
	initCtx_     api.Function
	run_         api.Function
}

func newVM(ctx context.Context, aot bool) (w *vm, err error) {
	src, err := fs.ReadFile("libddwaf.wasm")
	if err != nil {
		return nil, err
	}

	var cfg wazero.RuntimeConfig
	if aot {
		cfg = wazero.NewRuntimeConfigCompiler()
	} else {
		cfg = wazero.NewRuntimeConfigInterpreter()
	}
	rt := wazero.NewRuntimeWithConfig(cfg)
	defer func() {
		if err != nil && rt != nil {
			rt.Close(ctx)
		}
	}()

	_, err = wasi.InstantiateSnapshotPreview1(ctx, rt)
	if err != nil {
		return nil, err
	}

	compiled, err := rt.CompileModule(ctx, src, wazero.NewCompileConfig())
	if err != nil {
		return nil, err
	}
	module, err := rt.InstantiateModule(ctx, compiled, wazero.NewModuleConfig().WithStdout(os.Stdout).WithStderr(os.Stderr))
	if err != nil {
		return nil, err
	}

	memory := module.Memory()
	if memory == nil {
		return nil, fmt.Errorf("undefined memory")
	}
	malloc, err := exportedFunction(module, "malloc")
	if err != nil {
		return nil, err
	}
	free, err := exportedFunction(module, "free")
	if err != nil {
		return nil, err
	}
	init, err := exportedFunction(module, "ddwaf_init")
	if err != nil {
		return nil, err
	}
	encode, err := exportedFunction(module, "ddwaf_encode")
	if err != nil {
		return nil, err
	}
	enableLogger, err := exportedFunction(module, "my_ddwaf_set_logger")
	if err != nil {
		return nil, err
	}
	initCtx, err := exportedFunction(module, "ddwaf_context_init")
	if err != nil {
		return nil, err
	}
	run, err := exportedFunction(module, "my_ddwaf_run")
	if err != nil {
		return nil, err
	}

	return &vm{
		Runtime:      rt,
		memory:       memory,
		malloc_:      malloc,
		free_:        free,
		enableLogger: enableLogger,
		encode_:      encode,
		init_:        init,
		initCtx_:     initCtx,
		run_:         run,
	}, nil
}

func (w *vm) malloc(ctx context.Context, size uint64) (uint32, error) {
	ret, err := w.malloc_.Call(ctx, size)
	if err != nil {
		return 0, err
	}
	return uint32(ret[0]), nil
}

func (w *vm) free(ctx context.Context, addr uint32) error {
	_, err := w.free_.Call(ctx, uint64(addr))
	return err
}

func (w *vm) vmbytes(ctx context.Context, buf []byte) (uint32, error) {
	addr, err := w.malloc(ctx, uint64(len(buf))+1)
	if err != nil {
		return 0, err
	}

	// Copy the null-terminated buffer
	if ok := w.memory.Write(ctx, addr, buf); !ok {
		goto writeError
	}
	if ok := w.memory.WriteByte(ctx, addr+uint32(len(buf)), 0); !ok {
		goto writeError
	}

	return addr, nil

writeError:
	w.free(ctx, addr)
	return 0, fmt.Errorf("could not write the buffer into the vm's allocated memory area")
}

func (w *vm) gostring(ctx context.Context, vmbuf uint32) (string, error) {
	var (
		builder strings.Builder
		i       uint32
	)
	for {
		// TODO: optimize with vmbuf's strlen() + Read()
		b, ok := w.memory.ReadByte(ctx, vmbuf+i)
		if !ok {
			return builder.String(), fmt.Errorf("could not read the vm's memory at %X", vmbuf+i)
		}
		// Stop on null character
		if b == 0 {
			break
		}
		builder.WriteByte(b)
		i++
	}
	return builder.String(), nil
}

func (w *vm) encode(ctx context.Context, buf []byte) (uint32, error) {
	vmbuf, err := w.vmbytes(ctx, buf)
	if err != nil {
		return 0, err
	}
	ret, err := w.encode_.Call(ctx, uint64(vmbuf))
	if err != nil {
		return 0, err
	}
	return uint32(ret[0]), nil
}

func (w *vm) newInstance(ctx context.Context, rules uint32, withLogs bool) (instance uint32, err error) {
	if withLogs {
		w.enableLogger.Call(ctx)
	}
	ret, err := w.init_.Call(ctx, uint64(rules), 0, 0)
	if err != nil {
		return 0, err
	}
	return uint32(ret[0]), nil
}

func (w *vm) newContext(ctx context.Context, handle uint32) (wafCtx uint32, err error) {
	ret, err := w.initCtx_.Call(ctx, uint64(handle), 0)
	if err != nil {
		return 0, err
	}
	return uint32(ret[0]), nil
}

func (w *vm) run(ctx context.Context, wafCtx, inputs uint32) (events uint32, err error) {
	ret, err := w.run_.Call(ctx, uint64(wafCtx), uint64(inputs))
	if err != nil {
		return 0, err
	}
	return uint32(ret[0]), nil
}

func exportedFunction(module api.Module, name string) (fn api.Function, err error) {
	fn = module.ExportedFunction(name)
	if fn == nil {
		err = fmt.Errorf("undefined function `%s`", name)
	}
	return
}

func TestVM(t *testing.T) {
	vm, err := newVM(nil, false)
	require.NoError(t, err)
	defer vm.Close(nil)

	rules, err := vm.encode(nil, testRule)
	require.NoError(t, err)
	require.NotZero(t, rules)

	waf, err := vm.newInstance(nil, rules, true)
	require.NoError(t, err)
	require.NotZero(t, waf)

	wafCtx, err := vm.newContext(nil, waf)
	require.NoError(t, err)
	require.NotZero(t, wafCtx)

	inputs, err := vm.encode(nil, []byte(`{"addr": "Arachni"}`))
	require.NoError(t, err)
	require.NotZero(t, inputs)

	events, err := vm.run(nil, wafCtx, inputs)
	require.NoError(t, err)
	require.NotZero(t, events)

	str, err := vm.gostring(nil, events)
	require.NoError(t, err)
	require.NotEmpty(t, str)
	fmt.Println(str)
}

func BenchmarkVM(b *testing.B) {
	w, err := newVM(nil, false)
	require.NoError(b, err)
	defer w.Close(nil)

	rules, err := w.encode(nil, testRule)
	require.NoError(b, err)
	require.NotZero(b, rules)

	waf, err := w.newInstance(nil, rules, false)
	require.NoError(b, err)
	require.NotZero(b, waf)

	wafCtx, err := w.newContext(nil, waf)
	require.NoError(b, err)
	require.NotZero(b, wafCtx)

	data, err := w.encode(nil, []byte(`{"addr":"no match"}`))
	require.NoError(b, err)
	require.NotZero(b, data)

	b.ReportAllocs()
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		events, err := w.run(nil, wafCtx, data)
		if err != nil {
			b.Fatal(err)
		}
		if events != 0 {
			b.Fatal()
		}
	}
}

func BenchmarkCGO(b *testing.B) {
	handle, err := waf.NewHandle(testRule, "", "")
	require.NoError(b, err)
	wafCtx := waf.NewContext(handle)
	require.NotNil(b, wafCtx)

	// Not matching because the address is not used by the rule
	values := map[string]interface{}{
		"addr": "no match",
	}

	b.ReportAllocs()
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		matches, err := wafCtx.Run(values, time.Second)
		if err != nil {
			b.Fatal(err)
		}
		if len(matches) != 0 {
			b.Fatal()
		}
	}
}

var testRule = newTestRule(ruleInput{Address: "addr"})

var testRuleTmpl = template.Must(template.New("").Parse(`
{
  "version": "2.1",
  "rules": [
    {
      "id": "ua0-600-12x",
      "name": "Arachni",
      "tags": {
        "type": "security_scanner",
		"category": "attack_attempt"
      },
      "conditions": [
        {
          "operator": "match_regex",
          "parameters": {
            "inputs": [
            {{ range $i, $input := . -}}
              {{ if gt $i 0 }},{{ end }}
                { "address": "{{ $input.Address }}"{{ if ne (len $input.KeyPath) 0 }},  "key_path": [ {{ range $i, $path := $input.KeyPath }}{{ if gt $i 0 }}, {{ end }}"{{ $path }}"{{ end }} ]{{ end }} }
            {{- end }}
            ],
            "regex": "^Arachni"
          }
        }
      ],
      "transformers": []
    }
  ]
}
`))

type ruleInput struct {
	Address string
	KeyPath []string
}

func newTestRule(inputs ...ruleInput) []byte {
	var buf bytes.Buffer
	if err := testRuleTmpl.Execute(&buf, inputs); err != nil {
		panic(err)
	}
	return buf.Bytes()
}
