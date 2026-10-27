package plugins

import (
	"errors"
	"fmt"
	"strings"
	"sync"

	"cel.dev/cel-go/cel"
	"cel.dev/cel-go/common/types"
	"cel.dev/cel-go/common/types/ref"
	"cel.dev/cel-go/common/types/traits"
	"cel.dev/cel-go/ext"
)

// celCostLimit bounds one evaluation; a runaway expression fails instead of stalling the grid.
const celCostLimit = 1_000_000

var (
	celEnvOnce sync.Once
	celEnv     *cel.Env
	celEnvErr  error
)

// env is shared: it only declares `object` (the Kubernetes object as a map) and the string
// extensions. No function in it has side effects or reaches the network.
func env() (*cel.Env, error) {
	celEnvOnce.Do(func() {
		celEnv, celEnvErr = cel.NewEnv(cel.Variable("object", cel.MapType(cel.StringType, cel.DynType)), ext.Strings())
	})
	return celEnv, celEnvErr
}

// program is one compiled expression.
type program struct {
	src string
	prg cel.Program
}

func compile(expr string) (*program, error) {
	e, err := env()
	if err != nil {
		return nil, err
	}
	ast, iss := e.Compile(expr)
	if iss != nil && iss.Err() != nil {
		return nil, fmt.Errorf("CEL %q: %s", short(expr), strings.ReplaceAll(iss.Err().Error(), "\n", " "))
	}
	prg, err := e.Program(ast, cel.CostLimit(celCostLimit), cel.InterruptCheckFrequency(100))
	if err != nil {
		return nil, fmt.Errorf("CEL %q: %w", short(expr), err)
	}
	return &program{src: expr, prg: prg}, nil
}

// eval runs the expression on an object; a panic of the evaluator becomes an error.
func (p *program) eval(obj map[string]any) (v ref.Val, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("CEL falhou: %v", r)
		}
	}()
	v, _, err = p.prg.Eval(map[string]any{"object": obj})
	return v, err
}

// Bool evaluates a boolean expression; anything but true is false.
func (p *program) Bool(obj map[string]any) (bool, error) {
	v, err := p.eval(obj)
	if err != nil {
		return false, err
	}
	b, ok := v.(types.Bool)
	if !ok {
		return false, errors.New("a expressão não retornou bool")
	}
	return bool(b), nil
}

// Strings evaluates an expression returning a string, a list or a scalar, as display text.
func (p *program) Strings(obj map[string]any) ([]string, error) {
	v, err := p.eval(obj)
	if err != nil {
		return nil, err
	}
	switch t := native(v).(type) {
	case nil:
		return nil, nil
	case []any:
		out := make([]string, 0, len(t))
		for _, x := range t {
			out = append(out, fmt.Sprint(x))
		}
		return out, nil
	default:
		return []string{fmt.Sprint(t)}, nil
	}
}

// Value evaluates an expression for a grid cell (native Go value).
func (p *program) Value(obj map[string]any) (any, error) {
	v, err := p.eval(obj)
	if err != nil {
		return nil, err
	}
	return native(v), nil
}

// native converts a CEL value to plain Go values (nil, string, int64, float64, bool, []any,
// map[string]any); anything else becomes its text.
func native(v ref.Val) any {
	switch t := v.(type) {
	case types.Null:
		return nil
	case types.String, types.Int, types.Uint, types.Double, types.Bool:
		return t.Value()
	case traits.Lister:
		out := []any{}
		for it := t.Iterator(); it.HasNext() == types.True; {
			out = append(out, native(it.Next())) // a list iterator yields the elements
		}
		return out
	case traits.Mapper:
		out := map[string]any{}
		for it := t.Iterator(); it.HasNext() == types.True; {
			k := it.Next()
			out[fmt.Sprint(k.Value())] = native(t.Get(k))
		}
		return out
	}
	return fmt.Sprint(v.Value())
}

func short(s string) string {
	if len(s) > 60 {
		return s[:57] + "…"
	}
	return s
}
