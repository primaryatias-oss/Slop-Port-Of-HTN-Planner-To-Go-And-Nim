# Worker threads share the immutable planner definition and callterm
# registry: use atomic reference counting. Allocation statistics feed the
# allocs/plan columns.
switch("mm", "atomicArc")
switch("threads", "on")
switch("define", "nimAllocStats")
