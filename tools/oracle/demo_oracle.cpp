// htn-demo-oracle: a headless driver for the original HTNDemo.
//
// It links the demo's own world, daemon and agent sources (DemoGridTerrain,
// DemoWanderer, AIHTNDemoWandererAgent, AIHTNDemoPathfinder, ...) and replaces
// the SDL/ImGui front end with deterministic text output, which is the golden
// reference for the htn-demo commands of the Go and Nim ports
// (testdata/demo).
//
//   htn-demo-oracle runner [--rt]
//       Every demo domain and top-level method, run like the Domain Runner
//       tab: each backtracking mode on the domain's preferred world state,
//       then every world state with all backtracking enabled.
//   htn-demo-oracle simulate [--agents N] [--steps N] [--snapshot-every N] [--rt]
//       The NPC Simulation tab with a fixed 1/60 s step.
//
// Callterm error reports (stderr in the demo) are written to stdout so they
// stay ordered with the rest of the output.

#include "AI/AIHTNDemoWandererAgent.h"
#include "AI/AIHtnDaemonDemoTest.h"
#include "AI/AIHtnListDaemon.h"
#include "Core/HTNCallTermBinding.h"
#include "Core/HTNTask.h"
#include "Core/HtnSymbol.h"
#include "HTNDemoCallTermReporting.h"
#include "Hook/HTNDatabaseHook.h"
#include "Hook/HTNPlannerHook.h"
#include "Hook/HTNPlanningUnit.h"
#include "Translator/HTNGeneratedPlanner.h"
#include "World/DemoGridTerrain.h"
#include "World/DemoWanderer.h"

#include <algorithm>
#include <cstdio>
#include <cstring>
#include <filesystem>
#include <memory>
#include <sstream>
#include <string>
#include <unistd.h>
#include <vector>

#define DECLARE_PLANNER(Entry) extern "C" const HTNGeneratedPlannerDefinition* Entry##_GetDefinition(void);
#define DEMO_PLANNERS(X)                                                                                               \
    X(CreateAAACombatNPCHTN) X(CreateAtomListDemoHTN) X(CreateEliteNinjaHTN) X(CreateGruntHTN)                          \
    X(CreateNormalNinjaHTN) X(CreateCalltermsHTN) X(CreateComplexScenarioHTN) X(CreateHumanHTN)                        \
    X(CreateNestedCallsHTN) X(CreateIncludeDemoHTN) X(CreateWandererHTN) X(CreateBuiltinComparisonsDemoHTN)            \
    X(CreateHierarchicalBacktrackingHTN) X(CreateRuntimeBacktrackingDemoHTN) X(CreateNumericExpressionsHTN)            \
    X(CreateAAACombatNPCRTHTN) X(CreateAtomListDemoRTHTN) X(CreateEliteNinjaRTHTN) X(CreateGruntRTHTN)                  \
    X(CreateNormalNinjaRTHTN) X(CreateCalltermsRTHTN) X(CreateComplexScenarioRTHTN) X(CreateHumanRTHTN)                \
    X(CreateNestedCallsRTHTN) X(CreateIncludeDemoRTHTN) X(CreateWandererRTHTN)                                         \
    X(CreateBuiltinComparisonsDemoRTHTN) X(CreateHierarchicalBacktrackingRTHTN)                                        \
    X(CreateRuntimeBacktrackingDemoRTHTN) X(CreateNumericExpressionsRTHTN)
DEMO_PLANNERS(DECLARE_PLANNER)

namespace
{
struct Domain
{
    const char* Name;
    const HTNGeneratedPlannerDefinition* (*Definition)(void);
    const HTNGeneratedPlannerDefinition* (*RuntimeBacktrackingDefinition)(void);
    const char* Source;
    std::vector<const char*> Methods;
};

// The domain table of HTNDemo/src/main.cpp.
const Domain kDomains[] = {
    {"AAACombatNPC", CreateAAACombatNPCHTN_GetDefinition, CreateAAACombatNPCRTHTN_GetDefinition, "AAACombatNPC", {"run"}},
    {"AtomListDemo", CreateAtomListDemoHTN_GetDefinition, CreateAtomListDemoRTHTN_GetDefinition, "atom_list_demo",
     {"show_atom_list", "split_list_basic", "split_list_single_element", "split_list_empty_fails",
      "split_list_bound_outputs", "split_list_rollback", "split_list_front_basic", "split_list_back_basic",
      "split_list_back_bound_outputs"}},
    {"EliteNinja", CreateEliteNinjaHTN_GetDefinition, CreateEliteNinjaRTHTN_GetDefinition, "EliteNinja", {"run"}},
    {"Grunt", CreateGruntHTN_GetDefinition, CreateGruntRTHTN_GetDefinition, "Grunt", {"run"}},
    {"NormalNinja", CreateNormalNinjaHTN_GetDefinition, CreateNormalNinjaRTHTN_GetDefinition, "NormalNinja", {"run"}},
    {"CallTermsDemo", CreateCalltermsHTN_GetDefinition, CreateCalltermsRTHTN_GetDefinition, "callterms",
     {"test_callterms", "callterm_creates_fact_visible_immediately",
      "callterm_world_state_mutation_survives_backtracking"}},
    {"ComplexScenario", CreateComplexScenarioHTN_GetDefinition, CreateComplexScenarioRTHTN_GetDefinition,
     "complex_scenario", {"run_scenario"}},
    {"Human", CreateHumanHTN_GetDefinition, CreateHumanRTHTN_GetDefinition, "human", {"behave", "behave_upper_body"}},
    {"NestedCallsDemo", CreateNestedCallsHTN_GetDefinition, CreateNestedCallsRTHTN_GetDefinition, "nested_calls",
     {"test_nested_calls"}},
    {"IncludeDemo", CreateIncludeDemoHTN_GetDefinition, CreateIncludeDemoRTHTN_GetDefinition, "include_demo",
     {"run_include_demo"}},
    {"Wanderer", CreateWandererHTN_GetDefinition, CreateWandererRTHTN_GetDefinition, "Wanderer", {"run"}},
    {"BuiltinComparisonsDemo", CreateBuiltinComparisonsDemoHTN_GetDefinition,
     CreateBuiltinComparisonsDemoRTHTN_GetDefinition, "BuiltinComparisonsDemo", {"run"}},
    {"HierarchicalBacktracking", CreateHierarchicalBacktrackingHTN_GetDefinition,
     CreateHierarchicalBacktrackingRTHTN_GetDefinition, "hierarchical_backtracking",
     {"validate_parent_guard", "validate_child_guard", "validate_fact_backtracking", "validate_axiom_backtracking"}},
    {"RuntimeBacktrackingDemo", CreateRuntimeBacktrackingDemoHTN_GetDefinition,
     CreateRuntimeBacktrackingDemoRTHTN_GetDefinition, "RuntimeBacktrackingDemo",
     {"demo_fact_alternatives", "demo_axiom_alternatives", "demo_hierarchical_branches",
      "demo_direct_branch_fallback"}},
    {"NumericExpressionsDemo", CreateNumericExpressionsHTN_GetDefinition, CreateNumericExpressionsRTHTN_GetDefinition,
     "numeric_expressions", {"run", "division_by_zero", "invalid_operand_type"}},
};

// BindCalls of HTNDemo/src/main.cpp.
void BindCalls(HTNCallTermRegistry& r)
{
    AIHtnListDaemon::BindCallTerms(r);
    AIHTNDemoWandererAgent::BindCallTerms(r);
    AIHtnDaemonDemoTest::BindCallTerms(r);
    r.Bind("binded_function_with_args", [](const HTNCallTermArguments& a) { return a.size() == 1u; });
    r.Bind("get_health", [](const HTNCallTermArguments&) { return 50; });
    r.Bind("get_max_speed", [](const HTNCallTermArguments&) { return 1.0f; });
    r.Bind("lt", [](const HTNCallTermArguments& a) {
        return a.size() == 2u && HTNAtomIsType<int32>(a[0]) && HTNAtomIsType<int32>(a[1]) &&
               HTNAtomGetValue<int32>(a[0]) < HTNAtomGetValue<int32>(a[1]);
    });
    r.Bind("inc", [](const HTNCallTermArguments& a) {
        return a.size() == 1u && HTNAtomIsType<int32>(a[0]) ? HTNAtomGetValue<int32>(a[0]) + 1 : 0;
    });
    r.Bind("add", [](const HTNCallTermArguments& a) {
        return a.size() == 2u && HTNAtomIsType<int32>(a[0]) && HTNAtomIsType<int32>(a[1])
                   ? HTNAtomGetValue<int32>(a[0]) + HTNAtomGetValue<int32>(a[1])
                   : 0;
    });
    r.Bind("mul", [](const HTNCallTermArguments& a) {
        return a.size() == 2u && HTNAtomIsType<int32>(a[0]) && HTNAtomIsType<int32>(a[1])
                   ? HTNAtomGetValue<int32>(a[0]) * HTNAtomGetValue<int32>(a[1])
                   : 0;
    });
}

const char* StatusName(const HTNDecompositionStatus inStatus)
{
    switch (inStatus)
    {
    case HTN_DECOMPOSITION_SUCCEEDED: return "SUCCEEDED";
    case HTN_DECOMPOSITION_NO_PLAN: return "NO_PLAN";
    case HTN_DECOMPOSITION_BACKTRACKING_CAPACITY_EXCEEDED: return "BACKTRACKING_CAPACITY_EXCEEDED";
    case HTN_DECOMPOSITION_OUT_OF_MEMORY: return "OUT_OF_MEMORY";
    case HTN_DECOMPOSITION_INVALID_CONTEXT: return "INVALID_CONTEXT";
    case HTN_DECOMPOSITION_INVALID_CALL: return "INVALID_CALL";
    case HTN_DECOMPOSITION_PREPARATION_FAILED: return "PREPARATION_FAILED";
    case HTN_DECOMPOSITION_NOT_RUN: return "NOT_RUN";
    case HTN_DECOMPOSITION_CALL_FRAME_CAPACITY_EXCEEDED: return "CALL_FRAME_CAPACITY_EXCEEDED";
    }
    return "UNKNOWN";
}

const char* ModeName(const HTNBacktrackingMode inMode)
{
    switch (inMode)
    {
    case HTN_BACKTRACKING_NONE: return "None";
    case HTN_BACKTRACKING_FACTS_AND_AXIOMS: return "Facts and axioms";
    case HTN_BACKTRACKING_BRANCHES: return "Branches";
    case HTN_BACKTRACKING_ALL: return "All";
    }
    return "Unknown";
}

// FindWorldStates/BestWS/FormatTask of HTNDemo/src/main.cpp.
std::vector<std::filesystem::path> FindWorldStates()
{
    std::vector<std::filesystem::path> v;
    std::error_code e;
    const std::filesystem::path root = std::filesystem::path(HTN_PORT_ROOT) / "WorldStates";
    for (std::filesystem::recursive_directory_iterator i(root, e), end; i != end && !e; i.increment(e))
        if (i->is_regular_file() && i->path().extension() == ".worldstate")
            v.push_back(i->path());
    std::sort(v.begin(), v.end());
    return v;
}

int BestWS(const Domain& d, const std::vector<std::filesystem::path>& v)
{
    for (int i = 0; i < (int)v.size(); ++i)
    {
        auto s = v[i].stem().string();
        if (s == d.Source || s.starts_with(std::string(d.Source) + "_"))
            return i;
    }
    return v.empty() ? -1 : 0;
}

std::string FormatTask(const HTNAtomOwner& t)
{
    std::ostringstream s;
    auto* h = HTNGetTaskHead(t);
    s << (h ? h->GetString() : "<invalid>");
    for (uint32 i = 0; i < HTNGetTaskArgumentCount(t); ++i)
        s << ' ' << HTNAtomToString(HTNGetTaskArgument(t, i), true);
    return s.str();
}

std::string RelativeWorldState(const std::filesystem::path& inPath)
{
    return std::filesystem::relative(inPath, std::filesystem::path(HTN_PORT_ROOT)).generic_string();
}

int RunDomainRunner(const bool inRuntimeBacktracking)
{
    HTNCallTermRegistry calls;
    BindCalls(calls);
    HTNDatabaseHook database;
    const auto worldStates = FindWorldStates();
    const HTNBacktrackingMode modes[] = {HTN_BACKTRACKING_NONE, HTN_BACKTRACKING_FACTS_AND_AXIOMS,
                                         HTN_BACKTRACKING_BRANCHES, HTN_BACKTRACKING_ALL};
    for (const Domain& d : kDomains)
    {
        const HTNGeneratedPlannerDefinition* definition =
            inRuntimeBacktracking ? d.RuntimeBacktrackingDefinition() : d.Definition();
        for (const char* method : d.Methods)
        {
            // reload(): a fresh hook and planning unit per domain/method selection.
            auto planner = std::make_unique<HTNPlannerHook>(database.GetWorldState(), calls);
            std::unique_ptr<HTNPlanningUnit> unit;
            if (planner->SetGeneratedPlannerDefinition(definition))
            {
                unit = std::make_unique<HTNPlanningUnit>(database, *planner, method);
                unit->GetExecutionContext().CallTermErrorPolicy = HTNCallTermErrorPolicy::Report;
                unit->GetExecutionContext().CallTermErrorCallback = ReportGeneratedDemoCallTermError;
            }
            std::printf("domain %s method %s\n", d.Name, method);
            if (!unit)
            {
                std::printf("reload failed\n");
                continue;
            }
            const int best = BestWS(d, worldStates);
            std::vector<std::pair<int, HTNBacktrackingMode>> runs;
            for (const HTNBacktrackingMode mode : modes)
                runs.emplace_back(best, mode);
            for (int i = 0; i < (int)worldStates.size(); ++i)
                runs.emplace_back(i, HTN_BACKTRACKING_ALL);
            for (const auto& [index, mode] : runs)
            {
                unit->SetBacktrackingMode(mode);
                std::printf("run %s %s\n", RelativeWorldState(worldStates[index]).c_str(), ModeName(mode));
                if (!database.ParseWorldStateFile(worldStates[index].string()))
                {
                    std::printf("load failed\n");
                    continue;
                }
                const HTNDecompositionStatus status = unit->DecomposeTopLevelMethod(HtnSymbol::sGetSymbol(method));
                std::printf("status %s\n", StatusName(status));
                if (status == HTN_DECOMPOSITION_SUCCEEDED)
                {
                    const auto& output = unit->GetLastDecomposition().GetResult();
                    for (int32 i = 0; i < output.GetListSize(); ++i)
                        std::printf("step %s\n", FormatTask(HTNAtomOwner(output.GetListElement((uint32)i))).c_str());
                }
            }
        }
    }
    return 0;
}

std::string FormatCell(const Cell& inCell)
{
    return "(" + std::to_string(inCell.X) + " " + std::to_string(inCell.Y) + ")";
}

int RunSimulation(const int inAgents, const int inSteps, const int inSnapshotEvery, const bool inRuntimeBacktracking)
{
    HTNCallTermRegistry calls;
    BindCalls(calls);
    const HTNGeneratedPlannerDefinition* definition =
        inRuntimeBacktracking ? CreateWandererRTHTN_GetDefinition() : CreateWandererHTN_GetDefinition();
    DemoGridTerrain terrain;
    std::printf("terrain %d %d interactables %zu\n", terrain.GetWidth(), terrain.GetHeight(),
                terrain.GetInteractables().size());
    for (int y = terrain.GetHeight() - 1; y >= 0; --y)
    {
        std::string row;
        for (int x = 0; x < terrain.GetWidth(); ++x)
        {
            const DemoGridCellType type = terrain.GetCellType({x, y});
            row += type == DemoGridCellType::Blocked ? '#' : type == DemoGridCellType::Interactable ? 'o' : '.';
        }
        std::printf("map %2d %s\n", y, row.c_str());
    }
    for (const DemoGridInteractable& interactable : terrain.GetInteractables())
        std::printf("interactable %s %s %s %s %.9g\n", interactable.Id.c_str(),
                    DemoGridTerrain::GetInteractableTypeName(interactable.Type), FormatCell(interactable.Location).c_str(),
                    interactable.ContextAnimation, interactable.UsageTimeSeconds);

    // HTNNPCSimulationPanel::SpawnNPC: waypoint (id - 1) % WaypointCount.
    std::vector<std::unique_ptr<AIHTNDemoWandererAgent>> npcs;
    for (int index = 0; index < inAgents; ++index)
    {
        const std::uint32_t id = static_cast<std::uint32_t>(index + 1);
        auto npc = std::make_unique<AIHTNDemoWandererAgent>(id, definition, (id - 1u) % DemoWanderer::GetWaypointCount(),
                                                            terrain, calls);
        (void)npc->Initialize();
        npcs.emplace_back(std::move(npc));
    }
    if (!npcs.empty())
    {
        std::string waypoints;
        for (std::size_t i = 0; i < DemoWanderer::GetWaypointCount(); ++i)
            waypoints += " " + FormatCell(npcs.front()->GetWanderer().GetWaypointLocation(i));
        std::printf("waypoints%s\n", waypoints.c_str());
    }

    const float deltaTime = 1.0f / 60.0f;
    float simulationAge = 0.0f;
    const auto snapshot = [&](const int inStep) {
        std::printf("frame %d age %.9g\n", inStep, simulationAge);
        for (const auto& npc : npcs)
        {
            const DemoWanderer& wanderer = npc->GetWanderer();
            std::printf("npc %u %s at %s to %s task %s remaining %.9g plans %llu completed %llu journeys %llu ok %d\n",
                        npc->GetId(), wanderer.GetStateName(), FormatCell(wanderer.GetCurrentLocation()).c_str(),
                        FormatCell(wanderer.GetDestination()).c_str(), npc->GetCurrentTaskName(),
                        npc->GetCurrentTaskRemainingSeconds(), (unsigned long long)npc->GetPlanCount(),
                        (unsigned long long)npc->GetCompletedTaskCount(),
                        (unsigned long long)wanderer.GetJourneyCount(), npc->DidLastPlanSucceed() ? 1 : 0);
            const auto& plan = npc->GetCurrentPlan();
            for (std::size_t i = 0; i < plan.size(); ++i)
                std::printf("  %s%s\n", i == npc->GetCurrentTaskIndex() ? "> " : "  ",
                            npc->FormatTaskForDisplay(plan[i]).c_str());
        }
    };
    snapshot(0);
    for (int step = 1; step <= inSteps; ++step)
    {
        simulationAge += deltaTime;
        for (const auto& npc : npcs)
            npc->Update(deltaTime);
        if (inSnapshotEvery > 0 && step % inSnapshotEvery == 0)
            snapshot(step);
    }
    for (const auto& npc : npcs)
    {
        std::printf("history %u\n", npc->GetId());
        for (const auto& entry : npc->GetHistory())
            std::printf("  %.9g %s\n", entry.AgeSeconds, entry.Text.c_str());
    }
    return 0;
}
} // namespace

int main(int argc, char** argv)
{
    // Keep callterm error reports (stderr) ordered with stdout.
    std::setvbuf(stdout, nullptr, _IONBF, 0);
    dup2(STDOUT_FILENO, STDERR_FILENO);
    if (argc < 2)
    {
        std::printf("usage: htn-demo-oracle runner|simulate [options]\n");
        return 2;
    }
    bool runtimeBacktracking = false;
    int agents = 8, steps = 3600, snapshotEvery = 300;
    for (int i = 2; i < argc; ++i)
    {
        const std::string argument = argv[i];
        if (argument == "--rt")
            runtimeBacktracking = true;
        else if (argument == "--agents" && i + 1 < argc)
            agents = std::atoi(argv[++i]);
        else if (argument == "--steps" && i + 1 < argc)
            steps = std::atoi(argv[++i]);
        else if (argument == "--snapshot-every" && i + 1 < argc)
            snapshotEvery = std::atoi(argv[++i]);
        else
        {
            std::printf("unknown option %s\n", argument.c_str());
            return 2;
        }
    }
    if (std::strcmp(argv[1], "runner") == 0)
        return RunDomainRunner(runtimeBacktracking);
    if (std::strcmp(argv[1], "simulate") == 0)
        return RunSimulation(agents, steps, snapshotEvery, runtimeBacktracking);
    std::printf("unknown command %s\n", argv[1]);
    return 2;
}
