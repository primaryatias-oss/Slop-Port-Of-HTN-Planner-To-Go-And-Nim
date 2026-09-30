package callterm

import (
	"fmt"
	"math"
	"reflect"
	"sync"

	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/htn/atom"
)

// List is the native representation of an HTN list argument or result.
type List struct {
	atom.Atom
}

// Converter translates between atoms and one Go type. It is the counterpart of
// the HTNTypeTraits/HTNTypeConverter specializations of the original.
type Converter struct {
	// Name is reported as ErrorInfo.ExpectedTypeName.
	Name string
	// Fixed is true when the Go type always uses Kind.
	Fixed bool
	Kind  atom.Kind
	// FromAtom converts an argument. clientContext is the execution's borrowed
	// client context.
	FromAtom func(clientContext any, value atom.Atom) (reflect.Value, bool)
	// ToAtom converts a return value.
	ToAtom func(clientContext any, value reflect.Value) (atom.Atom, bool)
}

var converters = struct {
	sync.RWMutex
	byType map[reflect.Type]*Converter
}{byType: make(map[reflect.Type]*Converter)}

// RegisterConverter installs a converter for Go type T. kind is nil when T has
// no fixed atom representation. Register converters during initialization.
func RegisterConverter[T any](name string, kind *atom.Kind,
	from func(clientContext any, value atom.Atom) (T, bool),
	to func(clientContext any, value T) (atom.Atom, bool)) {
	c := &Converter{Name: name}
	if kind != nil {
		c.Fixed = true
		c.Kind = *kind
	}
	if from != nil {
		c.FromAtom = func(ctx any, value atom.Atom) (reflect.Value, bool) {
			v, ok := from(ctx, value)
			return reflect.ValueOf(&v).Elem(), ok
		}
	}
	if to != nil {
		c.ToAtom = func(ctx any, value reflect.Value) (atom.Atom, bool) {
			return to(ctx, value.Interface().(T))
		}
	}
	converters.Lock()
	converters.byType[reflect.TypeOf((*T)(nil)).Elem()] = c
	converters.Unlock()
}

func findConverter(t reflect.Type) *Converter {
	converters.RLock()
	defer converters.RUnlock()
	return converters.byType[t]
}

func kindPtr(k atom.Kind) *atom.Kind { return &k }

func init() {
	RegisterConverter[bool]("bool", kindPtr(atom.KindBool),
		func(_ any, a atom.Atom) (bool, bool) { return a.Bool(), a.Is(atom.KindBool) },
		func(_ any, v bool) (atom.Atom, bool) { return atom.NewBool(v), true })
	RegisterConverter[int32]("int32", kindPtr(atom.KindInt),
		func(_ any, a atom.Atom) (int32, bool) { return a.Int(), a.Is(atom.KindInt) },
		func(_ any, v int32) (atom.Atom, bool) { return atom.NewInt(v), true })
	RegisterConverter[int]("int32", kindPtr(atom.KindInt),
		func(_ any, a atom.Atom) (int, bool) { return int(a.Int()), a.Is(atom.KindInt) },
		func(_ any, v int) (atom.Atom, bool) {
			if v < math.MinInt32 || v > math.MaxInt32 {
				return atom.Atom{}, false
			}
			return atom.NewInt(int32(v)), true
		})
	RegisterConverter[float32]("float", kindPtr(atom.KindFloat),
		func(_ any, a atom.Atom) (float32, bool) { return a.Float(), a.Is(atom.KindFloat) },
		func(_ any, v float32) (atom.Atom, bool) { return atom.NewFloat(v), true })
	RegisterConverter[float64]("float", kindPtr(atom.KindFloat),
		func(_ any, a atom.Atom) (float64, bool) { return float64(a.Float()), a.Is(atom.KindFloat) },
		func(_ any, v float64) (atom.Atom, bool) { return atom.NewFloat(float32(v)), true })
	RegisterConverter[string]("string", kindPtr(atom.KindString),
		func(_ any, a atom.Atom) (string, bool) { return a.Str(), a.Is(atom.KindString) },
		func(_ any, v string) (atom.Atom, bool) { return atom.NewString(v), true })
	RegisterConverter[*atom.Symbol]("HtnSymbol", kindPtr(atom.KindSymbol),
		func(_ any, a atom.Atom) (*atom.Symbol, bool) { return a.Symbol(), a.Is(atom.KindSymbol) },
		func(_ any, v *atom.Symbol) (atom.Atom, bool) { return atom.NewSymbol(v), v != nil })
	RegisterConverter[atom.Atom]("HTNAtom", nil,
		func(_ any, a atom.Atom) (atom.Atom, bool) { return a, true },
		func(_ any, v atom.Atom) (atom.Atom, bool) { return v, true })
	RegisterConverter[List]("HTNAtomList", kindPtr(atom.KindList),
		func(_ any, a atom.Atom) (List, bool) { return List{a}, a.Is(atom.KindList) },
		func(_ any, v List) (atom.Atom, bool) {
			if !v.Is(atom.KindList) {
				return atom.Atom{}, false
			}
			return v.Atom, true
		})
}

type typedFunction struct {
	fn        reflect.Value
	params    []*Converter
	result    *Converter
	signature Signature
	offset    int // 1 when the first parameter is the daemon instance
}

func buildTypedFunction(fn any, hasDaemon bool) (*typedFunction, error) {
	value := reflect.ValueOf(fn)
	if value.Kind() != reflect.Func {
		return nil, fmt.Errorf("callterm binding must be a function, got %T", fn)
	}
	t := value.Type()
	if t.NumOut() != 1 {
		return nil, fmt.Errorf("callterm %T must return exactly one value", fn)
	}
	tf := &typedFunction{fn: value}
	if hasDaemon {
		if t.NumIn() < 1 {
			return nil, fmt.Errorf("member callterm %T must take the daemon as its first parameter", fn)
		}
		tf.offset = 1
	}
	for i := tf.offset; i < t.NumIn(); i++ {
		c := findConverter(t.In(i))
		if c == nil || c.FromAtom == nil {
			return nil, fmt.Errorf("no HTN type conversion registered for callterm argument type %s", t.In(i))
		}
		tf.params = append(tf.params, c)
		if c.Fixed {
			tf.signature = append(tf.signature, KindType(c.Kind))
		} else {
			tf.signature = append(tf.signature, AnyType())
		}
	}
	tf.result = findConverter(t.Out(0))
	if tf.result == nil || tf.result.ToAtom == nil {
		return nil, fmt.Errorf("no HTN type conversion registered for callterm return type %s", t.Out(0))
	}
	return tf, nil
}

func (tf *typedFunction) invoke(daemon any, args *Arguments) atom.Atom {
	in := make([]reflect.Value, 0, len(tf.params)+tf.offset)
	if tf.offset == 1 {
		daemonType := tf.fn.Type().In(0)
		daemonValue := reflect.ValueOf(daemon)
		if !daemonValue.IsValid() || !daemonValue.Type().AssignableTo(daemonType) {
			args.SetError(ReasonMissingInstance, NoIndex, NoIndex, "")
			return atom.Atom{}
		}
		in = append(in, daemonValue)
	}
	for i, c := range tf.params {
		v, ok := c.FromAtom(args.ClientContext(), args.At(i))
		if !ok {
			expected := uint32(NoIndex)
			if c.Fixed {
				expected = uint32(c.Kind)
			}
			args.SetError(ReasonArgumentConversionFailed, uint32(i), expected, c.Name)
			return atom.Atom{}
		}
		in = append(in, v)
	}
	out := tf.fn.Call(in)
	result, ok := tf.result.ToAtom(args.ClientContext(), out[0])
	if !ok {
		args.SetError(ReasonReturnConversionFailed, NoIndex, NoIndex, tf.result.Name)
		return atom.Atom{}
	}
	return result
}

// BindFunc registers a typed static callterm. fn is any Go function whose
// parameter and result types have registered converters, for example
// func(entity int32) float32. The signature is derived from the parameters.
func (r *Registry) BindFunc(id string, fn any) error {
	tf, err := buildTypedFunction(fn, false)
	if err != nil {
		return err
	}
	r.BindWithSignature(id, func(args *Arguments) atom.Atom { return tf.invoke(nil, args) }, tf.signature)
	return nil
}

// MustBindFunc is BindFunc that panics on invalid bindings.
func (r *Registry) MustBindFunc(id string, fn any) {
	if err := r.BindFunc(id, fn); err != nil {
		panic(err)
	}
}

// BindMemberFunc registers a typed stateful callterm. fn takes the daemon
// instance as its first parameter, for example func(d *Agent, x int32) bool.
// Each planner supplies the daemon with BindingContext.SetDaemon(daemonID, d).
func (r *Registry) BindMemberFunc(id, daemonID string, fn any) error {
	tf, err := buildTypedFunction(fn, true)
	if err != nil {
		return err
	}
	if !r.BindMember(id, daemonID, tf.invoke, tf.signature) {
		return fmt.Errorf("cannot register callterm daemon type [%s]: capacity [%d] has been reached", daemonID, MaxDaemonTypes)
	}
	return nil
}

// ToAtom converts a Go value through its registered converter.
func ToAtom(clientContext any, value any) (atom.Atom, bool) {
	if a, ok := value.(atom.Atom); ok {
		return a, true
	}
	v := reflect.ValueOf(value)
	if !v.IsValid() {
		return atom.Atom{}, false
	}
	c := findConverter(v.Type())
	if c == nil || c.ToAtom == nil {
		return atom.Atom{}, false
	}
	return c.ToAtom(clientContext, v)
}

// FromAtom converts an atom into *out through the converter registered for T.
func FromAtom[T any](clientContext any, value atom.Atom, out *T) bool {
	c := findConverter(reflect.TypeOf((*T)(nil)).Elem())
	if c == nil || c.FromAtom == nil {
		return false
	}
	v, ok := c.FromAtom(clientContext, value)
	if !ok {
		return false
	}
	*out = v.Interface().(T)
	return true
}
