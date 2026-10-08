package youtube

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/dop251/goja"
)

func evaluatePlayerNSig(ctx context.Context, script string, query url.Values, signatureKey, signature string) (url.Values, error) {
	vm := goja.New()
	result := cloneURLValues(query)
	searchParams := vm.NewObject()
	methods := map[string]any{
		"get": func(call goja.FunctionCall) goja.Value {
			value, ok := result[call.Argument(0).String()]
			if !ok || len(value) == 0 {
				return goja.Null()
			}
			return vm.ToValue(value[0])
		},
		"getAll": func(call goja.FunctionCall) goja.Value {
			return vm.ToValue(result[call.Argument(0).String()])
		},
		"has": func(call goja.FunctionCall) goja.Value {
			_, ok := result[call.Argument(0).String()]
			return vm.ToValue(ok)
		},
		"set": func(call goja.FunctionCall) goja.Value {
			result.Set(call.Argument(0).String(), call.Argument(1).String())
			return goja.Undefined()
		},
		"append": func(call goja.FunctionCall) goja.Value {
			key := call.Argument(0).String()
			result[key] = append(result[key], call.Argument(1).String())
			return goja.Undefined()
		},
		"delete": func(call goja.FunctionCall) goja.Value {
			result.Del(call.Argument(0).String())
			return goja.Undefined()
		},
		"toString": func(goja.FunctionCall) goja.Value {
			return vm.ToValue(result.Encode())
		},
	}
	for name, method := range methods {
		if err := searchParams.Set(name, method); err != nil {
			return nil, fmt.Errorf("set player search parameter method %q: %w", name, err)
		}
	}
	urlObject := vm.NewObject()
	if err := urlObject.Set("searchParams", searchParams); err != nil {
		return nil, fmt.Errorf("set player URL search parameters: %w", err)
	}
	for _, name := range []string{"get", "getAll", "has", "set", "append", "delete"} {
		if err := urlObject.Set(name, searchParams.Get(name)); err != nil {
			return nil, fmt.Errorf("set player URL method %q: %w", name, err)
		}
	}
	urlConstructor := func(call goja.ConstructorCall) *goja.Object {
		if input, ok := call.Argument(0).(*goja.Object); ok {
			return input
		}
		return call.This
	}
	if err := vm.Set("URL", urlConstructor); err != nil {
		return nil, fmt.Errorf("set player URL constructor: %w", err)
	}
	global := vm.NewObject()
	if err := global.Set("$R", urlConstructor); err != nil {
		return nil, fmt.Errorf("set player URL class: %w", err)
	}
	if err := vm.Set("g", global); err != nil {
		return nil, fmt.Errorf("set player global object: %w", err)
	}
	stopContextInterrupt := context.AfterFunc(ctx, func() { vm.Interrupt(ctx.Err()) })
	timer := time.AfterFunc(3*time.Second, func() { vm.Interrupt(errPlayerTimeout) })
	defer stopContextInterrupt()
	defer timer.Stop()
	if _, err := vm.RunString(script); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("compile player combined transform: %w", err)
	}
	function, ok := goja.AssertFunction(vm.Get("__youtubeNSig"))
	if !ok {
		return nil, errors.New("player combined transform is not callable")
	}
	_, err := function(goja.Undefined(), urlObject, vm.ToValue(signatureKey), vm.ToValue(signature))
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if errors.Is(err, errPlayerTimeout) {
			return nil, err
		}
		return nil, fmt.Errorf("run player combined transform: %w", err)
	}
	return result, nil
}
