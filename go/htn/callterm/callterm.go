// Package callterm connects domain (call ...) expressions to host functions.
//
// It ports HTNCallTermRegistry, HTNCallTermBindingContext and the unified
// callterm error policy of the original framework. A Registry is configured
// once and may then be shared read-only by any number of planners; each
// planner owns a BindingContext holding its daemon instances.
package callterm

import (
	"fmt"
	"math"

	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/htn/atom"
)

// NoIndex marks an unavailable index/count/type in ErrorInfo.
const NoIndex = math.MaxUint32

// MaxDaemonTypes is the number of distinct daemon types a registry accepts.
const MaxDaemonTypes = 32

// ErrorPolicy selects how invocation errors are handled.
type ErrorPolicy uint32

const (
	// PolicyUnset is the zero value. The original asserts in debug builds and
	// fails safely in release builds; this port always fails safely.
	PolicyUnset ErrorPolicy = iota
	// PolicyFailSilently fails the invocation without reporting.
	PolicyFailSilently
	// PolicyReport invokes the error callback, then fails the invocation.
	PolicyReport
)

// ErrorReason describes why an invocation failed.
type ErrorReason uint32

const (
	ReasonNotRegistered ErrorReason = iota
	ReasonMissingBinding
	ReasonMissingInstance
	ReasonArgumentCountMismatch
	ReasonArgumentTypeMismatch
	ReasonArgumentConversionFailed
	ReasonReturnConversionFailed
	ReasonNone ErrorReason = math.MaxUint32
)

var reasonNames = [...]string{
	"NotRegistered", "MissingBinding", "MissingInstance", "ArgumentCountMismatch",
	"ArgumentTypeMismatch", "ArgumentConversionFailed", "ReturnConversionFailed",
}

// String returns the reason name.
func (r ErrorReason) String() string {
	if int(r) < len(reasonNames) {
		return reasonNames[r]
	}
	if r == ReasonNone {
		return "None"
	}
	return fmt.Sprintf("ErrorReason(%d)", uint32(r))
}

// Source is the domain provenance of a call site. Empty strings stand for the
// null pointers of the original C structure.
type Source struct {
	Domain string
	File   string
	Line   uint32
	Column uint32
}

// ErrorInfo describes one failed invocation. It is only valid during the
// callback.
type ErrorInfo struct {
	Name                  string
	Reason                ErrorReason
	DaemonID              string
	Source                Source
	ArgumentIndex         uint32
	ExpectedArgumentCount uint32
	ActualArgumentCount   uint32
	ExpectedAtomType      uint32
	ActualAtomType        uint32
	ExpectedTypeName      string
}

// ErrorCallback receives invocation errors under PolicyReport.
type ErrorCallback func(clientContext any, info *ErrorInfo)

// Arguments is the borrowed argument view passed to callterm functions.
type Arguments struct {
	values        []atom.Atom
	clientContext any
	err           *ErrorInfo
}

// NewArguments creates an argument view over values (for direct execution).
func NewArguments(values ...atom.Atom) *Arguments { return &Arguments{values: values} }

// Len returns the argument count.
func (a *Arguments) Len() int { return len(a.values) }

// At returns argument i.
func (a *Arguments) At(i int) atom.Atom { return a.values[i] }

// Values returns every argument (read-only).
func (a *Arguments) Values() []atom.Atom { return a.values }

// ClientContext returns the borrowed client context of the current execution.
func (a *Arguments) ClientContext() any { return a.clientContext }

// SetError records the first invocation error; the registry reports it after
// the function returns.
func (a *Arguments) SetError(reason ErrorReason, index uint32, expectedType uint32, expectedName string) {
	if a.err == nil || a.err.Reason != ReasonNone {
		return
	}
	a.err.Reason = reason
	a.err.ArgumentIndex = index
	a.err.ExpectedAtomType = expectedType
	a.err.ExpectedTypeName = expectedName
	if int(index) < len(a.values) && index != NoIndex {
		a.err.ActualAtomType = uint32(a.values[index].Kind())
	}
}

// Function is a raw callterm implementation. daemon is the planner's daemon
// instance for member bindings (nil for static bindings). Returning an
// unbound atom fails the call without an error report.
type Function func(daemon any, args *Arguments) atom.Atom

// SignatureType is one expected argument type; Any accepts every atom type.
type SignatureType struct {
	Any  bool
	Kind atom.Kind
}

// Signature lists the expected argument types of a typed binding.
type Signature []SignatureType

// AnyType accepts any atom.
func AnyType() SignatureType { return SignatureType{Any: true} }

// KindType accepts exactly kind k.
func KindType(k atom.Kind) SignatureType { return SignatureType{Kind: k} }

// Entry is one registered callterm.
type Entry struct {
	Name         string
	Function     Function
	Signature    Signature
	HasSignature bool
	DaemonSlot   int
	DaemonID     string
}

// Registry maps callterm names to implementations.
type Registry struct {
	entries     map[string]*Entry
	daemonSlots map[string]int
}

// NewRegistry creates an empty registry.
func NewRegistry() *Registry {
	return &Registry{entries: make(map[string]*Entry), daemonSlots: make(map[string]int)}
}

// Bind registers a raw, untyped callterm. The function receives every argument
// unchanged.
func (r *Registry) Bind(id string, fn func(args *Arguments) atom.Atom) {
	var wrapped Function
	if fn != nil {
		wrapped = func(_ any, args *Arguments) atom.Atom { return fn(args) }
	}
	r.store(id, Entry{Name: id, Function: wrapped, DaemonSlot: -1})
}

// store installs an entry. Rebinding replaces the existing entry in place, so
// callterm slots already resolved by generated planners observe the new
// binding (the original keeps registry entries at stable addresses).
func (r *Registry) store(id string, entry Entry) {
	if existing := r.entries[id]; existing != nil {
		*existing = entry
		return
	}
	r.entries[id] = &entry
}

// BindWithSignature registers a callterm whose arguments are validated against
// signature before fn runs.
func (r *Registry) BindWithSignature(id string, fn func(args *Arguments) atom.Atom, signature Signature) {
	var wrapped Function
	if fn != nil {
		wrapped = func(_ any, args *Arguments) atom.Atom {
			if !validateArguments(signature, args) {
				return atom.Atom{}
			}
			return fn(args)
		}
	}
	r.store(id, Entry{Name: id, Function: wrapped, Signature: signature, HasSignature: true, DaemonSlot: -1})
}

// BindMember registers a stateful callterm whose daemon instance is supplied
// by each planner's BindingContext. It fails when the daemon-type capacity is
// exhausted.
func (r *Registry) BindMember(id, daemonID string, fn Function, signature Signature) bool {
	slot, ok := r.daemonSlots[daemonID]
	if !ok {
		if len(r.daemonSlots) >= MaxDaemonTypes {
			return false
		}
		slot = len(r.daemonSlots)
		r.daemonSlots[daemonID] = slot
	}
	var wrapped Function
	if fn != nil {
		wrapped = func(daemon any, args *Arguments) atom.Atom {
			if !validateArguments(signature, args) {
				return atom.Atom{}
			}
			return fn(daemon, args)
		}
	}
	r.store(id, Entry{Name: id, Function: wrapped, Signature: signature, HasSignature: true,
		DaemonSlot: slot, DaemonID: daemonID})
	return true
}

// IsBound reports whether id has a registered implementation.
func (r *Registry) IsBound(id string) bool {
	entry := r.entries[id]
	return entry != nil && entry.Function != nil
}

// Resolve returns the entry registered for id (possibly without a function).
func (r *Registry) Resolve(id string) *Entry { return r.entries[id] }

func (r *Registry) findDaemonSlot(id string) int {
	if slot, ok := r.daemonSlots[id]; ok {
		return slot
	}
	return -1
}

func validateArguments(signature Signature, args *Arguments) bool {
	if args.Len() != len(signature) {
		args.SetError(ReasonArgumentCountMismatch, NoIndex, NoIndex, "")
		return false
	}
	for i, expected := range signature {
		if !expected.Any && args.At(i).Kind() != expected.Kind {
			args.SetError(ReasonArgumentTypeMismatch, uint32(i), uint32(expected.Kind), "")
			return false
		}
	}
	return true
}

func checkEntry(entry *Entry, bindings *BindingContext) (any, ErrorReason) {
	if entry == nil {
		return nil, ReasonNotRegistered
	}
	if entry.Function == nil {
		return nil, ReasonMissingBinding
	}
	if entry.DaemonSlot >= 0 {
		var daemon any
		if bindings != nil {
			daemon = bindings.daemon(entry.DaemonSlot)
		}
		if daemon == nil {
			return nil, ReasonMissingInstance
		}
		return daemon, ReasonNone
	}
	return nil, ReasonNone
}

// Invocation carries the per-execution options used when invoking callterms.
type Invocation struct {
	Bindings      *BindingContext
	ClientContext any
	Policy        ErrorPolicy
	Callback      ErrorCallback
}

// InvokeEntry runs one callterm and applies the error policy. It returns an
// unbound atom when the invocation failed or the callable returned unbound.
func InvokeEntry(entry *Entry, name string, args []atom.Atom, source *Source, inv *Invocation) atom.Atom {
	info := ErrorInfo{
		Name:                  name,
		Reason:                ReasonNone,
		ArgumentIndex:         NoIndex,
		ExpectedArgumentCount: NoIndex,
		ActualArgumentCount:   uint32(len(args)),
		ExpectedAtomType:      NoIndex,
		ActualAtomType:        NoIndex,
	}
	if entry != nil {
		info.DaemonID = entry.DaemonID
		if entry.HasSignature {
			info.ExpectedArgumentCount = uint32(len(entry.Signature))
		}
	}
	if source != nil {
		info.Source = *source
	}
	var result atom.Atom
	daemon, reason := checkEntry(entry, inv.Bindings)
	if reason != ReasonNone {
		info.Reason = reason
	} else {
		arguments := &Arguments{values: args, clientContext: inv.ClientContext, err: &info}
		result = entry.Function(daemon, arguments)
	}
	if info.Reason == ReasonNone {
		return result
	}
	if inv.Policy == PolicyReport && inv.Callback != nil {
		inv.Callback(inv.ClientContext, &info)
	}
	return atom.Atom{}
}

// Execute invokes the callterm registered as id (HTNCallTermRegistry::Execute).
func (r *Registry) Execute(id string, inv *Invocation, args []atom.Atom, source *Source) atom.Atom {
	return InvokeEntry(r.entries[id], id, args, source, inv)
}

// Requirement is one call site required by a generated domain.
type Requirement struct {
	Name   string
	Source Source
}

// ValidateRequirements checks every call site without executing callterms.
// It reports NotRegistered, MissingBinding and MissingInstance through the
// callback (when non-nil) and returns whether all call sites are satisfied.
// It returns false when bindings belongs to another registry.
func (r *Registry) ValidateRequirements(requirements []Requirement, bindings *BindingContext,
	callback ErrorCallback, clientContext any) bool {
	if bindings == nil || bindings.registry != r {
		return false
	}
	valid := true
	for _, requirement := range requirements {
		entry := r.entries[requirement.Name]
		if _, reason := checkEntry(entry, bindings); reason != ReasonNone {
			valid = false
			if callback != nil {
				info := ErrorInfo{Name: requirement.Name, Reason: reason, Source: requirement.Source,
					ArgumentIndex: NoIndex, ExpectedArgumentCount: NoIndex, ActualArgumentCount: NoIndex,
					ExpectedAtomType: NoIndex, ActualAtomType: NoIndex}
				if entry != nil {
					info.DaemonID = entry.DaemonID
				}
				callback(clientContext, &info)
			}
		}
	}
	return valid
}

// BindingContext holds one planner's daemon instances for a shared registry.
type BindingContext struct {
	registry *Registry
	daemons  [MaxDaemonTypes]any
}

// NewBindingContext creates a binding context for registry. A nil registry is
// replaced by an empty one.
func NewBindingContext(registry *Registry) *BindingContext {
	if registry == nil {
		registry = NewRegistry()
	}
	return &BindingContext{registry: registry}
}

// Registry returns the shared registry.
func (b *BindingContext) Registry() *Registry { return b.registry }

// SetDaemon installs the daemon instance for daemon type id.
func (b *BindingContext) SetDaemon(id string, daemon any) bool {
	slot := b.registry.findDaemonSlot(id)
	if slot < 0 || slot >= MaxDaemonTypes {
		return false
	}
	b.daemons[slot] = daemon
	return true
}

func (b *BindingContext) daemon(slot int) any {
	if slot < 0 || slot >= MaxDaemonTypes {
		return nil
	}
	return b.daemons[slot]
}

// Slot is a callterm resolved for generated execution: the registry entry is
// cached while the name is retained for reporting.
type Slot struct {
	Entry *Entry
	Name  string
}

// ResolveSlot resolves name through the binding context's registry.
func (b *BindingContext) ResolveSlot(name string) Slot {
	if b == nil {
		return Slot{Name: name}
	}
	return Slot{Entry: b.registry.entries[name], Name: name}
}
