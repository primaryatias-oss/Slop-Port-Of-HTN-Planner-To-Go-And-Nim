//go:build !htndebug

package planner

// DebugEnabled compiles generated execution events into generated planners
// ("htndebug" build tag).
const DebugEnabled = false
