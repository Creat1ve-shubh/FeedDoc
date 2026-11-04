//go:build js && wasm

// Command feeddoctor-wasm runs the checker inside the browser, so a
// merchant's product file never leaves their computer. It registers one
// global, feeddoctor, with three functions that take and return JSON:
//
//	feeddoctor.scanCSV(csvText, optionsJSON)   -> webscan.Result
//	feeddoctor.fixCSV(csvText, optionsJSON)    -> webscan.FixResult
//	feeddoctor.scanStore(inputJSON, optionsJSON) -> webscan.Result
//
// Each returns {"error": "..."} instead when the input cannot be read.
//
// Build it with web/build.sh.
package main

import (
	"encoding/json"
	"syscall/js"

	"github.com/Creat1ve-shubh/FeedDoc/internal/storefront"
	"github.com/Creat1ve-shubh/FeedDoc/internal/webscan"
)

func main() {
	js.Global().Set("feeddoctor", js.ValueOf(map[string]any{
		"scanCSV": js.FuncOf(func(_ js.Value, args []js.Value) any {
			return call(args, func(text string, opt webscan.Options) (any, error) {
				return webscan.ScanCSV(text, opt)
			})
		}),
		"fixCSV": js.FuncOf(func(_ js.Value, args []js.Value) any {
			return call(args, func(text string, opt webscan.Options) (any, error) {
				return webscan.FixCSV(text, opt)
			})
		}),
		"scanStore": js.FuncOf(func(_ js.Value, args []js.Value) any {
			return call(args, func(text string, opt webscan.Options) (any, error) {
				var in storefront.Input
				if err := json.Unmarshal([]byte(text), &in); err != nil {
					return nil, err
				}
				return webscan.ScanStore(in, opt)
			})
		}),
	}))
	select {} // keep the functions alive
}

func call(args []js.Value, fn func(string, webscan.Options) (any, error)) any {
	if len(args) < 1 {
		return errJSON("missing input")
	}
	var opt webscan.Options
	if len(args) > 1 && args[1].Type() == js.TypeString && args[1].String() != "" {
		if err := json.Unmarshal([]byte(args[1].String()), &opt); err != nil {
			return errJSON("bad options: " + err.Error())
		}
	}
	out, err := fn(args[0].String(), opt)
	if err != nil {
		return errJSON(err.Error())
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return errJSON(err.Error())
	}
	return string(raw)
}

func errJSON(msg string) string {
	raw, _ := json.Marshal(map[string]string{"error": msg})
	return string(raw)
}
