#!/usr/bin/env python3
"""Generates testdata/scenarios/sweep_*.scn.

Every sweep runs each entry method of a domain against a set of world states,
through the planning-unit path ("call" + "resolve") and the raw generated
entry point ("raw", which also reports call-frame usage). Runtime-backtracking
variants repeat the calls in every backtracking mode.

Run from the repository root:  python3 tools/scenarios/gen_sweeps.py
"""
import os

ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
OUT = os.path.join(ROOT, "testdata", "scenarios")
MODES = ["none", "facts_and_axioms", "branches", "all"]


def ws(path):
    return [f"worldstate {path}"]


def facts(*lines):
    return [f"fact {line}" for line in lines]


EMPTY = ("empty", [])

# family -> (variants, [(label, setup lines)], [calls])
FAMILIES = {
    "aaa_combat_npc": (["AAACombatNPC", "AAACombatNPCRT"],
        [(name, ws(f"WorldStates/AAACombatNPC_{name}.worldstate")) for name in
         ["high_order", "melee", "ranged", "medium_order", "investigation", "search", "low_order", "idle"]] + [EMPTY],
        ["run"]),
    "builtin_comparisons": (["BuiltinComparisonsDemo", "BuiltinComparisonsDemoRT"],
        [(name, ws(f"WorldStates/Test/builtin_comparisons_{name}.worldstate")) for name in
         ["critical", "engage", "out_of_ammo", "safe"]] + [EMPTY],
        ["run"]),
    "ninjas": (["EliteNinja", "EliteNinjaRT", "Grunt", "GruntRT", "NormalNinja", "NormalNinjaRT"],
        [("elite_ninja", ws("WorldStates/Test/elite_ninja.worldstate")), EMPTY],
        ["run"]),
    "runtime_backtracking_demo": (["RuntimeBacktrackingDemo", "RuntimeBacktrackingDemoRT"],
        [("demo", ws("WorldStates/RuntimeBacktrackingDemo.worldstate")), EMPTY],
        ["demo_fact_alternatives", "demo_axiom_alternatives", "demo_hierarchical_branches",
         "demo_direct_branch_fallback"]),
    "atom_list_demo": (["AtomListDemo", "AtomListDemoRT"],
        [("demo", ws("WorldStates/Test/atom_list_demo.worldstate")), EMPTY],
        ["show_atom_list", "split_list_basic", "split_list_single_element", "split_list_empty_fails",
         "split_list_bound_outputs", "split_list_rollback", "split_list_front_basic",
         "split_list_back_basic", "split_list_back_bound_outputs"]),
    "axiom_assignments": (["AxiomAssignments", "AxiomAssignmentsRT"],
        [("candidates", facts("entity 1", "candidate 1", "candidate 2")),
         ("other_entity", facts("entity 5", "candidate 3", "candidate 1", "candidate 2")), EMPTY],
        ["regression", "literal", "arithmetic", "solutions", "restore_alternative", "restore_caller",
         "bound_output", "literal_output", "arithmetic_output", "io_unbound", "io_bound", "io_nested_bound",
         "io_nested_unbound", "nested_forwarding", "io_restore_caller", "io_solutions",
         "io_restore_alternative", "io_alt_retry", "io_exhausted", "io_bound_different",
         "io_nested_bound_different", "io_literal", "io_literal_bound", "io_arithmetic_bound"]),
    "axiom_overloads": (["AxiomOverloads", "AxiomOverloadsRT"],
        [("overloads", ws("WorldStates/Test/axiom_overloads.worldstate")), EMPTY],
        ["run", "mismatch", "io_mismatch", "backtrack", "io_backtrack", "io_bound_backtrack"]),
    "backtracking_policy": (["BacktrackingPolicy", "BacktrackingPolicyRT", "BacktrackingPolicyOverflow",
                             "BacktrackingPolicyOverflowRT", "BacktrackingPolicyFixedSmall",
                             "BacktrackingPolicyFixedSmallRT", "BacktrackingPolicyFixedEnough",
                             "BacktrackingPolicyFixedEnoughRT"],
        [EMPTY],
        ["run", "run_small", "always_fail", "run"]),
    "callterms": (["Callterms", "CalltermsRT"],
        [("callterms", ws("WorldStates/Test/callterms.worldstate")), EMPTY],
        ["test_callterms", 'echo_top_level 7 "leader" guard_post', "echo_top_level 1.5 role (1 2)",
         "echo_top_level 1", "callterm_creates_fact_visible_immediately",
         "callterm_world_state_mutation_survives_backtracking", "test_callterms"]),
    "complex_scenario": (["ComplexScenario", "ComplexScenarioRT", "ComplexScenarioSmallFrames"],
        [(name, ws(f"WorldStates/Test/complex_scenario_{name}.worldstate")) for name in
         ["combat", "emergency", "idle", "mobility", "recovery", "recursive_100"]] + [EMPTY],
        ["run_scenario"]),
    "hierarchical_backtracking": (["HierarchicalBacktracking", "HierarchicalBacktrackingRT"],
        [("no_combat", ws("WorldStates/Test/hierarchical_backtracking_no_combat.worldstate")), EMPTY],
        ["validate_parent_guard", "validate_child_guard", "do_behavior_parent_guard enemy",
         "do_behavior_child_guard enemy", "do_behavior_parent_guard friend", "do_behavior_child_guard 3",
         "validate_fact_backtracking", "validate_axiom_backtracking"]),
    "human": (["Human", "HumanRT"],
        [("human", ws("WorldStates/Test/human.worldstate")),
         ("axiom_backtracking", ws("WorldStates/Test/human_axiom_backtracking.worldstate")), EMPTY],
        ["behave", "behave_upper_body", "behave 1"]),
    "method_overloads": (["MethodOverloads", "MethodOverloadsRT"],
        [EMPTY],
        ["run", "run 42", "run 1 2", "run", "mixed", "mixed 9", "deferred", "deferred 23", "defer_mixed",
         "later", "later 5"]),
    "missing_callterms": (["MissingCallterms", "MissingCalltermsRT"],
        [EMPTY, ("values", facts("value 1", "value 2", "target \"a\""))],
        ["condition", "binding", "primitive", "compound", "nested", "deferred", "unused"]),
    "nested_axiom_choices": (["NestedAxiomChoices", "NestedAxiomChoicesRT"],
        [("candidates", facts("candidate 1", "candidate 2", "first_candidate 1")),
         ("reversed", facts("candidate 2", "candidate 1", "candidate 3", "first_candidate 2")), EMPTY],
        ["out_backtrack", "io_backtrack", "deep_backtrack", "first_solution", "io_bound", "io_mismatch",
         "exhausted", "no_candidates", "internal_filter", "pair_backtrack", "pair_bound", "two_calls",
         "string_backtrack", "out_literal", "out_arithmetic", "out_bound", "out_mismatch",
         "out_owned_literal", "alt_preserves_bound", "alias_outputs", "nested_and", "alt_choice", "or_cut",
         "not_scope", "effects", "effects_exhausted", "effects_nested", "qualified_or",
         "effects_alt_exhausted"]),
    "nested_calls": (["NestedCalls", "NestedCallsRT"],
        [("callterms", ws("WorldStates/Test/callterms.worldstate")), EMPTY],
        ["test_nested_calls"]),
    "nested_operator_calls": (["NestedOperatorCalls", "NestedOperatorCallsRT"],
        [("active_plan", facts("active_plan 1 moving_to_seen_entity 42 10")),
         ("other_plan", facts("active_plan 1 moving_to_seen_entity 7 3", "active_plan 2 idle 42 10")), EMPTY],
        ["behave", "missing_right", "missing_arithmetic", "missing_deep", "bound_first", "two_attempts",
         "valid_left", "valid_right", "valid_both", "valid_bound", "operators", "short_circuit",
         "task_arithmetic", "missing_task_arithmetic", "continue_move", "debugger_backtracking",
         "debugger_skipped", "debugger_assignment"]),
    "numeric_expressions": (["NumericExpressions", "NumericExpressionsRT"],
        [("numeric", ws("WorldStates/Test/numeric_expressions.worldstate")),
         ("assignment", facts("assignment_candidate 1", "assignment_candidate 2")), EMPTY],
        ["run", "division_by_zero", "invalid_operand_type", "argument_expressions",
         "axiom_argument_expressions", "axiom_output_expression", "axiom_output_mismatch", "axiom_io_mismatch",
         "axiom_invalid_input_expression", "axiom_invalid_output_expression", "assignment_values",
         "assignment_backtracking", "assignment_nested_calls", "assignment_failure",
         "assignment_ordered_failure", "assignment_axiom"]),
    "recursion_dispatch": (["RecursionDispatch", "RecursionDispatchRT", "RecursionDispatchSmallFrames"],
        [("depth_2", facts("depth 2")), ("depth_20", facts("depth 20")), ("depth_1000", facts("depth 1000")),
         ("depth_5000", facts("depth 5000")), EMPTY],
        ["non_tail", "mutual", "deep_failure", "non_tail"]),
    "worldstate_lookup": (["WorldstateLookupScenarios", "WorldstateLookupScenariosRT"],
        [("lookup", ws("WorldStates/Test/worldstate_lookup_scenarios.worldstate")), EMPTY],
        ["run_scenario"]),
    "wanderer": (["Wanderer", "WandererRT"],
        [("wanderer", ws("WorldStates/Wanderer.worldstate")),
         ("even_cell", facts("wanderer_location (2 4)")), ("odd_cell", facts("wanderer_location (3 5)")), EMPTY],
        ["run", "talk_about_destination"]),
    "include_demo": (["IncludeDemo", "IncludeDemoRT"],
        [("elite_ninja", ws("WorldStates/Test/elite_ninja.worldstate")), EMPTY],
        ["run_include_demo"]),
}

# Deferred-only entry points are reached through rawdeferred.
DEFERRED = {"later", "talk_about_destination"}


def calls_for(calls, raw):
    lines = []
    for call in calls:
        head = call.split()[0]
        if head in DEFERRED:
            lines.append(f"rawdeferred {call}")
            continue
        lines.append(f"call {call}")
        lines.append("resolve")
        if raw:
            lines.append(f"raw {call}")
            lines.append(f"rawdeferred {call}")
    return lines


def main():
    for family, (variants, worlds, calls) in FAMILIES.items():
        out = [f"# Generated by tools/scenarios/gen_sweeps.py; do not edit.", ""]
        for variant in variants:
            runtime = variant.endswith("RT")
            for label, setup in worlds:
                out.append(f"scenario {variant}/{label}")
                out.append(f"planner {variant}")
                out.append("policy report")
                out.extend(setup)
                if runtime:
                    for mode in MODES:
                        out.append(f"mode {mode}")
                        out.extend(calls_for(calls, raw=(mode == "all")))
                else:
                    out.extend(calls_for(calls, raw=True))
                out.append("call no_such_method")
                out.append("end")
                out.append("")
        with open(os.path.join(OUT, f"sweep_{family}.scn"), "w") as f:
            f.write("\n".join(out))


if __name__ == "__main__":
    main()
