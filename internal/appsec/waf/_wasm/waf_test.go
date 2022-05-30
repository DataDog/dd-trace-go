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
	"github.com/tetratelabs/wazero/wasi"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/waf"
)

//go:embed libddwaf.wasm
var fs embed.FS

func TestVM(t *testing.T) {
	ctx := context.Background()
	buf, err := fs.ReadFile("libddwaf.wasm")
	if err != nil {
		panic(err)
	}
	rt := wazero.NewRuntimeWithConfig(wazero.NewRuntimeConfigInterpreter())
	code, err := rt.CompileModule(ctx, buf, wazero.NewCompileConfig())
	if err != nil {
		panic(err)
	}
	wm, err := wasi.InstantiateSnapshotPreview1(ctx, rt)
	require.NoError(t, err)
	defer wm.Close(ctx)
	config := wazero.NewModuleConfig().WithStdout(os.Stdout)
	module, err := rt.InstantiateModule(ctx, code, config)
	require.NoError(t, err)
	defer module.Close(ctx)
	memory := module.Memory()
	free := module.ExportedFunction("free")
	init := module.ExportedFunction("ddwaf_init")

	malloc := module.ExportedFunction("malloc")
	vmbuf := func(buf []byte) uint32 {
		ret, err := malloc.Call(nil, uint64(len(buf)+1))
		require.NoError(t, err)
		addr := uint32(ret[0])
		ok := memory.Write(ctx, addr, buf)
		require.True(t, ok)
		memory.WriteByte(ctx, addr+uint32(len(buf)), 0)
		return addr
	}

	encode := module.ExportedFunction("ddwaf_encode")
	vmencode := func(buf []byte) uint32 {
		addr := vmbuf(buf)
		defer free.Call(nil, uint64(addr))
		ret, err := encode.Call(nil, uint64(addr))
		require.NoError(t, err)
		return uint32(ret[0])
	}

	rule := vmencode(testRule)
	require.NotZero(t, rule)

	module.ExportedFunction("my_ddwaf_set_logger").Call(nil)

	ret, err := init.Call(nil, uint64(rule), 0, 0)
	require.NoError(t, err)
	handle := ret[0]

	ret, err = module.ExportedFunction("ddwaf_context_init").Call(nil, handle, 0)
	require.NoError(t, err)
	wafCtx := ret[0]

	run := module.ExportedFunction("my_ddwaf_run")
	data := vmencode([]byte(`{"addr": "Arachni"}`))
	ret, err = run.Call(nil, wafCtx, uint64(data))
	require.NoError(t, err)
	events := ret[0]
	require.NotZero(t, events)

	gostring := func(addr uint32) string {
		var (
			builder strings.Builder
			i       uint32
		)
		for {
			b, ok := memory.ReadByte(ctx, addr+i)
			require.True(t, ok)
			if b == 0 {
				break
			}
			builder.WriteByte(b)
			i++
		}
		return builder.String()
	}
	fmt.Println(gostring(uint32(events)))
}

func BenchmarkVM(b *testing.B) {
	ctx := context.Background()
	buf, err := fs.ReadFile("libddwaf.wasm")
	if err != nil {
		panic(err)
	}
	rt := wazero.NewRuntimeWithConfig(wazero.NewRuntimeConfigCompiler())
	code, err := rt.CompileModule(ctx, buf, wazero.NewCompileConfig())
	if err != nil {
		panic(err)
	}
	wm, err := wasi.InstantiateSnapshotPreview1(ctx, rt)
	require.NoError(b, err)
	defer wm.Close(ctx)
	config := wazero.NewModuleConfig().WithStdout(os.Stdout)
	module, err := rt.InstantiateModule(ctx, code, config)
	require.NoError(b, err)
	defer module.Close(ctx)
	memory := module.Memory()
	free := module.ExportedFunction("free")
	init := module.ExportedFunction("ddwaf_init")

	malloc := module.ExportedFunction("malloc")
	vmbuf := func(buf []byte) uint32 {
		ret, err := malloc.Call(nil, uint64(len(buf)+1))
		require.NoError(b, err)
		addr := uint32(ret[0])
		ok := memory.Write(ctx, addr, buf)
		require.True(b, ok)
		memory.WriteByte(ctx, addr+uint32(len(buf)), 0)
		return addr
	}

	encode := module.ExportedFunction("ddwaf_encode")
	vmencode := func(buf []byte) uint32 {
		addr := vmbuf(buf)
		defer free.Call(nil, uint64(addr))
		ret, err := encode.Call(nil, uint64(addr))
		require.NoError(b, err)
		return uint32(ret[0])
	}

	rule := vmencode(testRule)
	require.NotZero(b, rule)

	ret, err := init.Call(nil, uint64(rule), 0, 0)
	require.NoError(b, err)
	handle := ret[0]

	ret, err = module.ExportedFunction("ddwaf_context_init").Call(nil, handle, 0)
	require.NoError(b, err)
	wafCtx := ret[0]

	data := vmencode([]byte(`{"addr": "no match"}`))
	run := module.ExportedFunction("my_ddwaf_run")
	b.ReportAllocs()
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		ret, err = run.Call(nil, wafCtx, uint64(data))
		if err != nil {
			b.Fatal(err)
		}
		if events := ret[0]; events != 0 {
			b.Fatal()
		}
	}
}

func BenchmarkNative(b *testing.B) {
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
