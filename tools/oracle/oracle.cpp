// htn-oracle: runs HTN scenario files against the original C++ planner.
//
// The scenario language is shared with the Go and Nim ports (see
// testdata/scenarios/README.md). This program is the reference
// implementation: its output is committed as the golden files the ports are
// tested against.
//
//   htn-oracle <scenario-file>...
//
// Paths inside scenarios are relative to the port repository root.

#include "HTNPlanner.h"
#include "HTNIntegration.h"
#include "AI/AIHtnListDaemon.h"
#include "Core/HTNCallTermBinding.h"
#include "Core/HTNFileReader.h"
#include "Core/HTNTask.h"
#include "Core/HtnSymbol.h"
#include "Hook/HTNDatabaseHook.h"
#include "Hook/HTNPlannerHook.h"
#include "Hook/HTNPlanningUnit.h"
#include "Parser/HTNToken.h"
#include "Translator/HTNGeneratedPlanner.h"
#include "WorldState/Parser/HTNWorldStateLexer.h"
#include "WorldState/Parser/HTNWorldStateLexerContext.h"
#include "WorldState/Parser/HTNWorldStateParser.h"
#include "WorldState/Parser/HTNWorldStateParserContext.h"

#include <cmath>
#include <cstdio>
#include <cstdlib>
#include <fstream>
#include <memory>
#include <new>
#include <set>
#include <sstream>
#include <string>
#include <vector>

struct OraclePlanner
{
    const char* Name;
    const HTNGeneratedPlannerDefinition* (*GetDefinition)(void);
};

#include "OracleRegistry.inc"

namespace
{
std::string GOutput;

void Emit(const std::string& inLine)
{
    GOutput += inLine;
    GOutput += '\n';
}

std::string FormatAtom(const HTNAtom& inAtom)
{
    if (!HTNAtom_IsBound(&inAtom))
        return "?";
    return HTNAtomToString(inAtom, true);
}

std::string FormatStep(const HTNAtom* inStep)
{
    const HtnSymbol* Head = HTNGetCallHead(inStep);
    std::string Text = Head ? Head->GetString() : "<invalid>";
    const uint32 Count = HTNGetCallArgumentCount(inStep);
    for (uint32 Index = 0u; Index < Count; ++Index)
    {
        const HTNAtom* Argument = HTNFindCallArgument(inStep, Index);
        Text += " ";
        Text += Argument ? FormatAtom(*Argument) : "?";
    }
    return Text;
}

void EmitPlan(const HTNAtom& inPlan)
{
    if (HTNAtom_GetType(&inPlan) != HTN_ATOM_TYPE_LIST)
    {
        Emit("plan -");
        return;
    }
    const int32 Count = HTNAtom_GetListSize(&inPlan);
    Emit("plan " + std::to_string(Count));
    for (int32 Index = 0; Index < Count; ++Index)
        Emit("step " + FormatStep(HTNAtom_GetListElement(&inPlan, static_cast<uint32>(Index))));
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

const char* ReasonName(const HTNCallTermErrorReason inReason)
{
    switch (inReason)
    {
    case HTNCallTermErrorReason::NotRegistered: return "NotRegistered";
    case HTNCallTermErrorReason::MissingBinding: return "MissingBinding";
    case HTNCallTermErrorReason::MissingInstance: return "MissingInstance";
    case HTNCallTermErrorReason::ArgumentCountMismatch: return "ArgumentCountMismatch";
    case HTNCallTermErrorReason::ArgumentTypeMismatch: return "ArgumentTypeMismatch";
    case HTNCallTermErrorReason::ArgumentConversionFailed: return "ArgumentConversionFailed";
    case HTNCallTermErrorReason::ReturnConversionFailed: return "ReturnConversionFailed";
    case HTNCallTermErrorReason::None: return "None";
    }
    return "Unknown";
}

std::string Index(const uint32_t inValue)
{
    return inValue == UINT32_MAX ? std::string("-") : std::to_string(inValue);
}

std::string Text(const char* inValue)
{
    return inValue ? std::string(inValue) : std::string("-");
}

void OnCallTermError(void*, const HTNCallTermErrorInfo* inInfo)
{
    Emit("error " + std::string(ReasonName(inInfo->Reason)) + " name=" + Text(inInfo->Name) +
         " daemon=" + Text(inInfo->DaemonID) + " source=" + Text(inInfo->Source.domain) + "|" +
         Text(inInfo->Source.file) + ":" + std::to_string(inInfo->Source.line) + ":" +
         std::to_string(inInfo->Source.column) + " arg=" + Index(inInfo->ArgumentIndex) +
         " expected_count=" + Index(inInfo->ExpectedArgumentCount) + " actual_count=" +
         Index(inInfo->ActualArgumentCount) + " expected_type=" + Index(inInfo->ExpectedAtomType) +
         " actual_type=" + Index(inInfo->ActualAtomType) + " type_name=" + Text(inInfo->ExpectedTypeName));
}

void Trace(const std::string& inName, const HTNCallTermArguments& inArguments, const HTNAtomOwner& inResult)
{
    std::string Line = "trace " + inName + "(";
    for (size_t Index = 0u; Index < inArguments.size(); ++Index)
    {
        if (Index != 0u)
            Line += ", ";
        Line += FormatAtom(inArguments[Index]);
    }
    Line += ") -> ";
    Line += inResult.Get() ? FormatAtom(*inResult.Get()) : std::string("?");
    Emit(Line);
}

bool IsInt(const HTNCallTermArguments& inArguments, const size_t inIndex)
{
    return HTNAtom_GetType(&inArguments[inIndex]) == HTN_ATOM_TYPE_INT;
}

int32 IntAt(const HTNCallTermArguments& inArguments, const size_t inIndex)
{
    return inArguments[inIndex].value.int_value;
}

bool ReadCell(const HTNAtom& inAtom, int32& outX, int32& outY)
{
    if (HTNAtom_GetType(&inAtom) != HTN_ATOM_TYPE_LIST || HTNAtom_GetListSize(&inAtom) != 2)
        return false;
    const HTNAtom* X = HTNAtom_GetListElement(&inAtom, 0u);
    const HTNAtom* Y = HTNAtom_GetListElement(&inAtom, 1u);
    if (!X || !Y || HTNAtom_GetType(X) != HTN_ATOM_TYPE_INT || HTNAtom_GetType(Y) != HTN_ATOM_TYPE_INT)
        return false;
    outX = X->value.int_value;
    outY = Y->value.int_value;
    return true;
}

using StandardFunction = HTNAtomOwner (*)(const HTNCallTermArguments&);

struct StandardCallTerm
{
    const char* Name;
    StandardFunction Function;
};

// The standard scenario callterms. Every implementation is total: invalid
// arguments produce a neutral value, never an error.
const StandardCallTerm kStandardCallTerms[] = {
    {"binded_function_with_args", [](const HTNCallTermArguments& A) {
        return HTNAtomOwner(A.size() == 1u && HTNAtom_GetType(&A[0]) == HTN_ATOM_TYPE_STRING); }},
    {"get_health", [](const HTNCallTermArguments& A) {
        return HTNAtomOwner(int32{A.size() == 1u && IsInt(A, 0) ? 50 : 0}); }},
    {"get_max_speed", [](const HTNCallTermArguments& A) {
        return HTNAtomOwner(A.size() == 1u && IsInt(A, 0) ? 1.0f : 0.0f); }},
    {"lt", [](const HTNCallTermArguments& A) {
        return HTNAtomOwner(A.size() == 2u && IsInt(A, 0) && IsInt(A, 1) && IntAt(A, 0) < IntAt(A, 1)); }},
    {"inc", [](const HTNCallTermArguments& A) {
        return HTNAtomOwner(int32{A.size() == 1u && IsInt(A, 0) ? IntAt(A, 0) + 1 : 0}); }},
    {"add", [](const HTNCallTermArguments& A) {
        return HTNAtomOwner(int32{A.size() == 2u && IsInt(A, 0) && IsInt(A, 1) ? IntAt(A, 0) + IntAt(A, 1) : 0}); }},
    {"mul", [](const HTNCallTermArguments& A) {
        return HTNAtomOwner(int32{A.size() == 2u && IsInt(A, 0) && IsInt(A, 1) ? IntAt(A, 0) * IntAt(A, 1) : 0}); }},
    {"assignment_probe", [](const HTNCallTermArguments& A) {
        return HTNAtomOwner(int32{A.size() == 1u && IsInt(A, 0) ? IntAt(A, 0) + 1 : 0}); }},
    {"get_entity_position", [](const HTNCallTermArguments& A) {
        if (A.size() != 1u || !IsInt(A, 0))
            return HTNAtomOwner(int32{0});
        const int32 Entity = IntAt(A, 0);
        return HTNAtomOwner(int32{Entity == 1 ? 7 : Entity == 42 ? 20 : Entity + 100}); }},
    {"get_distance_from_to", [](const HTNCallTermArguments& A) {
        if (A.size() != 2u || !IsInt(A, 0) || !IsInt(A, 1))
            return HTNAtomOwner(0.0f);
        return HTNAtomOwner(static_cast<float>(std::abs(IntAt(A, 1) - IntAt(A, 0))) / 100.0f); }},
    {"distance", [](const HTNCallTermArguments&) { return HTNAtomOwner(0.1f); }},
    {"identity", [](const HTNCallTermArguments& A) {
        return A.size() == 1u ? HTNAtomOwner(A[0]) : HTNAtomOwner(); }},
    {"probe", [](const HTNCallTermArguments&) { return HTNAtomOwner(true); }},
    {"axiom_trace", [](const HTNCallTermArguments&) { return HTNAtomOwner(true); }},
    {"axiom_value", [](const HTNCallTermArguments&) { return HTNAtomOwner(int32{42}); }},
    {"visit", [](const HTNCallTermArguments&) { return HTNAtomOwner(true); }},
    {"same_location", [](const HTNCallTermArguments& A) {
        int32 FromX = 0, FromY = 0, ToX = 0, ToY = 0;
        return HTNAtomOwner(A.size() == 2u && ReadCell(A[0], FromX, FromY) && ReadCell(A[1], ToX, ToY) &&
                            FromX == ToX && FromY == ToY); }},
    {"both_coordinates_even", [](const HTNCallTermArguments& A) {
        int32 X = 0, Y = 0;
        return HTNAtomOwner(A.size() == 1u && ReadCell(A[0], X, Y) && X % 2 == 0 && Y % 2 == 0); }},
    {"both_coordinates_odd", [](const HTNCallTermArguments& A) {
        int32 X = 0, Y = 0;
        return HTNAtomOwner(A.size() == 1u && ReadCell(A[0], X, Y) && X % 2 != 0 && Y % 2 != 0); }},
};

StandardFunction FindStandard(const std::string& inName)
{
    for (const StandardCallTerm& CallTerm : kStandardCallTerms)
        if (inName == CallTerm.Name)
            return CallTerm.Function;
    return nullptr;
}

std::optional<HTNAtomType> ParseSignatureType(const std::string& inText, bool& outValid)
{
    outValid = true;
    if (inText == "any") return std::nullopt;
    if (inText == "bool") return HTN_ATOM_TYPE_BOOL;
    if (inText == "int") return HTN_ATOM_TYPE_INT;
    if (inText == "float") return HTN_ATOM_TYPE_FLOAT;
    if (inText == "string") return HTN_ATOM_TYPE_STRING;
    if (inText == "symbol") return HTN_ATOM_TYPE_SYMBOL;
    if (inText == "list") return HTN_ATOM_TYPE_LIST;
    outValid = false;
    return std::nullopt;
}

std::vector<std::string> Split(const std::string& inText, const char inSeparator)
{
    std::vector<std::string> Parts;
    std::string Part;
    std::istringstream Stream(inText);
    while (std::getline(Stream, Part, inSeparator))
        Parts.push_back(Part);
    return Parts;
}

// Owns the world state the add_target_available callterm writes into.
struct WorldStateDaemon
{
    HTNWorldState* World = nullptr;
};

struct AgentDaemon
{
    int32 Value = 1;
};

bool ParseFacts(const std::string& inText, HTNWorldState& ioWorldState)
{
    std::vector<HTNToken> Tokens;
    HTNWorldStateLexer Lexer;
    HTNWorldStateLexerContext LexerContext(inText, Tokens);
    if (!Lexer.Lex(LexerContext))
        return false;
    HTNWorldStateParser Parser;
    HTNWorldStateParserContext ParserContext(Tokens, ioWorldState);
    return Parser.Parse(ParserContext);
}

// Parses "name arg..." with the world-state syntax and builds (name arg...).
bool MakeCall(const std::string& inText, HTNAtom& outCall)
{
    HTNAtom_Init(&outCall);
    HTNWorldState Temporary;
    if (!ParseFacts(inText, Temporary))
        return false;
    const HTNFacts& Facts = Temporary.GetFacts();
    if (Facts.size() != 1u)
        return false;
    const auto& [Symbol, Tables] = *Facts.begin();
    for (size_t Arity = 0u; Arity < Tables.size(); ++Arity)
    {
        const HTNFactArgumentsCollection& Rows = Tables[Arity].GetFactArgumentsCollection();
        if (Rows.empty())
            continue;
        std::vector<HTNAtom> Arguments(Arity);
        for (size_t Index = 0u; Index < Arity; ++Index)
        {
            HTNAtom_Init(&Arguments[Index]);
            HTNAtom_Copy(&Arguments[Index], Rows[0][Index].Get());
        }
        const int Created = HTNAtom_CreateCall(&outCall, Symbol, Arguments.data(), static_cast<uint32_t>(Arity));
        for (HTNAtom& Argument : Arguments)
            HTNAtom_Destroy(&Argument);
        return Created != 0;
    }
    return false;
}

struct Scenario
{
    std::string Name;
    std::vector<std::string> CallTermSpec{"standard"};
    const HTNGeneratedPlannerDefinition* Definition = nullptr;
    std::unique_ptr<HTNDatabaseHook> Database;
    std::unique_ptr<HTNCallTermRegistry> Registry;
    std::unique_ptr<HTNPlannerHook> Hook;
    std::unique_ptr<HTNPlanningUnit> Unit;
    WorldStateDaemon WorldDaemon;
    AgentDaemon Agent;
    HTNBacktrackingMode Mode = HTN_BACKTRACKING_ALL;
    HTNCallTermErrorPolicy Policy = HTNCallTermErrorPolicy::FailSilently;
    void* RawPrepared = nullptr;
    void* RawExecution = nullptr;

    ~Scenario()
    {
        if (RawExecution)
        {
            Definition->destroy_execution_storage(RawExecution);
            ::operator delete(RawExecution);
        }
        if (RawPrepared)
        {
            Definition->destroy_prepared_storage(RawPrepared);
            ::operator delete(RawPrepared);
        }
        Unit.reset();
        Hook.reset();
        Registry.reset();
        Database.reset();
    }
};

[[noreturn]] void Fail(const std::string& inFile, const int inLine, const std::string& inMessage)
{
    std::fprintf(stderr, "%s:%d: %s\n", inFile.c_str(), inLine, inMessage.c_str());
    std::exit(2);
}

void BindCallTerms(Scenario& ioScenario, const std::string& inFile, const int inLine)
{
    HTNCallTermRegistry& Registry = *ioScenario.Registry;
    std::set<std::string> Excluded;
    bool Standard = false;
    for (const std::string& Item : ioScenario.CallTermSpec)
    {
        if (Item == "standard")
            Standard = true;
        else if (Item == "none")
            Standard = false;
        else if (!Item.empty() && Item[0] == '-')
            Excluded.insert(Item.substr(1));
    }
    if (Standard)
    {
        if (!Excluded.count("list"))
            AIHtnListDaemon::BindCallTerms(Registry);
        for (const StandardCallTerm& CallTerm : kStandardCallTerms)
        {
            if (Excluded.count(CallTerm.Name))
                continue;
            const std::string Name = CallTerm.Name;
            const StandardFunction Function = CallTerm.Function;
            Registry.Bind(Name, [Name, Function](const HTNCallTermArguments& inArguments) -> HTNAtomOwner {
                HTNAtomOwner Result = Function(inArguments);
                Trace(Name, inArguments, Result);
                return Result;
            });
        }
        if (!Excluded.count("add_target_available"))
        {
            Registry.BindMember("add_target_available", "WorldStateDaemon",
                [](void* inDaemon, const HTNCallTermArguments& inArguments) -> HTNAtomOwner {
                    auto* Daemon = static_cast<WorldStateDaemon*>(inDaemon);
                    const std::vector<HTNAtomOwner> Fact{HTNAtomOwner(std::string("enemy0"))};
                    Daemon->World->AddFact("target_available", Fact);
                    HTNAtomOwner Result(true);
                    Trace("add_target_available", inArguments, Result);
                    return Result;
                }, {});
        }
    }
    for (const std::string& Item : ioScenario.CallTermSpec)
    {
        const std::vector<std::string> Parts = Split(Item, ':');
        if (Parts.size() < 2u)
            continue;
        const std::string& Kind = Parts[0];
        const std::string& Name = Parts[1];
        if (Kind == "typed")
        {
            const StandardFunction Function = FindStandard(Name);
            if (!Function || Parts.size() != 3u)
                Fail(inFile, inLine, "typed callterm needs a standard name and a signature: " + Item);
            HTNCallTermSignature Signature;
            if (Parts[2] != "none")
            {
                for (const std::string& Type : Split(Parts[2], ','))
                {
                    bool Valid = false;
                    Signature.push_back(ParseSignatureType(Type, Valid));
                    if (!Valid)
                        Fail(inFile, inLine, "unknown signature type: " + Type);
                }
            }
            Registry.Bind(Name, [Name, Function](const HTNCallTermArguments& inArguments) -> HTNAtomOwner {
                HTNAtomOwner Result = Function(inArguments);
                Trace(Name, inArguments, Result);
                return Result;
            }, Signature);
        }
        else if (Kind == "member")
        {
            Registry.BindMember(Name, "agent",
                [Name](void* inDaemon, const HTNCallTermArguments& inArguments) -> HTNAtomOwner {
                    HTNAtomOwner Result(int32{static_cast<AgentDaemon*>(inDaemon)->Value});
                    Trace(Name, inArguments, Result);
                    return Result;
                }, {});
        }
        else if (Kind == "empty")
        {
            Registry.BindMember(Name, "agent", {}, {});
        }
        else
        {
            Fail(inFile, inLine, "unknown callterm modifier: " + Item);
        }
    }
}

void CreatePlanner(Scenario& ioScenario, const std::string& inVariant, const std::string& inFile, const int inLine)
{
    for (const OraclePlanner& Planner : kOraclePlanners)
    {
        if (inVariant == Planner.Name)
            ioScenario.Definition = Planner.GetDefinition();
    }
    if (!ioScenario.Definition)
        Fail(inFile, inLine, "unknown planner variant " + inVariant);
    ioScenario.Database = std::make_unique<HTNDatabaseHook>();
    ioScenario.Registry = std::make_unique<HTNCallTermRegistry>();
    BindCallTerms(ioScenario, inFile, inLine);
    ioScenario.Hook = std::make_unique<HTNPlannerHook>(ioScenario.Database->GetWorldState(), *ioScenario.Registry);
    ioScenario.WorldDaemon.World = &ioScenario.Database->GetWorldState();
    (void)ioScenario.Hook->GetCallTermBindingContext().SetDaemon("WorldStateDaemon", &ioScenario.WorldDaemon);
    if (!ioScenario.Hook->SetGeneratedPlannerDefinition(ioScenario.Definition))
        Fail(inFile, inLine, "definition rejected: " + inVariant);
    ioScenario.Unit = std::make_unique<HTNPlanningUnit>(*ioScenario.Database, *ioScenario.Hook, "run");
    ioScenario.Unit->SetBacktrackingMode(ioScenario.Mode);
    ioScenario.Unit->GetExecutionContext().CallTermErrorPolicy = ioScenario.Policy;
    ioScenario.Unit->GetExecutionContext().CallTermErrorCallback = &OnCallTermError;
}

void ReplaceAll(std::string& ioText, const std::string& inFrom, const std::string& inTo)
{
    for (size_t Position = ioText.find(inFrom); Position != std::string::npos;
         Position = ioText.find(inFrom, Position + inTo.size()))
        ioText.replace(Position, inFrom.size(), inTo);
}

void RunRaw(Scenario& ioScenario, const std::string& inCallText, const bool inRequireTopLevel,
            const std::string& inFile, const int inLine)
{
    const HTNGeneratedPlannerDefinition* Definition = ioScenario.Definition;
    if (!ioScenario.RawPrepared)
    {
        ioScenario.RawPrepared = ::operator new(Definition->prepared_storage_size);
        if (!Definition->initialize_prepared_storage(ioScenario.RawPrepared))
            Fail(inFile, inLine, "prepared storage initialization failed");
        ioScenario.RawExecution = ::operator new(Definition->execution_storage_size);
        if (!Definition->initialize_execution_storage(ioScenario.RawExecution))
            Fail(inFile, inLine, "execution storage initialization failed");
    }
    HTNAtom Call;
    if (!MakeCall(inCallText, Call))
        Fail(inFile, inLine, "invalid call: " + inCallText);
    HTNGeneratedPlannerContext Context{};
    Context.world_state = &ioScenario.Database->GetWorldState();
    Context.callterm_binding_context = &ioScenario.Hook->GetCallTermBindingContext();
    Context.backtracking_mode = ioScenario.Mode;
    Context.execution_storage = ioScenario.RawExecution;
    Context.prepared_storage = ioScenario.RawPrepared;
    Context.callterm_error_policy = ioScenario.Policy;
    Context.callterm_error_callback = &OnCallTermError;
    HTNAtom Result;
    const HTNDecompositionStatus Status = Definition->decompose_call(&Context, &Call, inRequireTopLevel ? 1 : 0, &Result);
    Emit(std::string("status ") + StatusName(Status));
    const HTNGeneratedExecutionInfo* Info = Definition->get_execution_info(ioScenario.RawExecution);
    std::string Error = Info->last_error ? Info->last_error : "-";
    ReplaceAll(Error, "HTNTranslator", "htn-translator");
    Emit("info peak=" + std::to_string(Info->peak_call_frames) + " capacity=" +
         std::to_string(Info->call_frame_capacity) + " error=" + Error);
    EmitPlan(Result);
    HTNAtom_Destroy(&Result);
    HTNAtom_Destroy(&Call);
}

std::string Trim(const std::string& inText)
{
    const size_t Begin = inText.find_first_not_of(" \t\r");
    if (Begin == std::string::npos)
        return std::string();
    const size_t End = inText.find_last_not_of(" \t\r");
    return inText.substr(Begin, End - Begin + 1u);
}

void RunFile(const std::string& inFile)
{
    std::ifstream Stream(inFile);
    if (!Stream)
        Fail(inFile, 0, "cannot open scenario file");
    std::unique_ptr<Scenario> Current;
    std::string Line;
    int LineNumber = 0;
    const std::string Root = HTN_PORT_ROOT;
    auto Finish = [&]() {
        if (Current)
        {
            Emit("end");
            Current.reset();
        }
    };
    while (std::getline(Stream, Line))
    {
        ++LineNumber;
        const std::string Trimmed = Trim(Line);
        if (Trimmed.empty() || Trimmed[0] == '#')
            continue;
        const size_t Space = Trimmed.find(' ');
        const std::string Command = Trimmed.substr(0, Space);
        const std::string Argument = Space == std::string::npos ? std::string() : Trim(Trimmed.substr(Space + 1u));
        if (Command == "scenario")
        {
            Finish();
            Current = std::make_unique<Scenario>();
            Current->Name = Argument;
            Emit("scenario " + Argument);
            continue;
        }
        if (!Current)
            Fail(inFile, LineNumber, "command outside of a scenario");
        Scenario& S = *Current;
        const bool NeedsPlanner = Command != "callterms" && Command != "planner" && Command != "end";
        if (NeedsPlanner && !S.Unit)
            Fail(inFile, LineNumber, "'" + Command + "' requires a planner");
        if (Command == "callterms")
        {
            if (S.Unit)
                Fail(inFile, LineNumber, "callterms must precede planner");
            S.CallTermSpec = Split(Argument, ' ');
        }
        else if (Command == "planner")
        {
            CreatePlanner(S, Argument, inFile, LineNumber);
        }
        else if (Command == "worldstate")
        {
            const bool Parsed = S.Database->ParseWorldStateFile(Root + "/" + Argument);
            if (!Parsed)
                Emit("worldstate failed " + Argument);
        }
        else if (Command == "fact")
        {
            if (!ParseFacts(Argument, S.Database->GetWorldState()))
                Emit("fact failed " + Argument);
        }
        else if (Command == "remove_fact")
        {
            const std::vector<std::string> Parts = Split(Argument, ' ');
            if (Parts.size() != 3u)
                Fail(inFile, LineNumber, "remove_fact <name> <arity> <index>");
            S.Database->GetWorldState().RemoveFact(Parts[0], std::stoul(Parts[1]), std::stoul(Parts[2]));
        }
        else if (Command == "clear_facts")
        {
            S.Database->GetWorldState().RemoveAllFacts();
        }
        else if (Command == "mode")
        {
            if (Argument == "none") S.Mode = HTN_BACKTRACKING_NONE;
            else if (Argument == "facts_and_axioms") S.Mode = HTN_BACKTRACKING_FACTS_AND_AXIOMS;
            else if (Argument == "branches") S.Mode = HTN_BACKTRACKING_BRANCHES;
            else if (Argument == "all") S.Mode = HTN_BACKTRACKING_ALL;
            else Fail(inFile, LineNumber, "unknown mode " + Argument);
            S.Unit->SetBacktrackingMode(S.Mode);
        }
        else if (Command == "policy")
        {
            if (Argument == "unset") S.Policy = HTNCallTermErrorPolicy::Unset;
            else if (Argument == "silent") S.Policy = HTNCallTermErrorPolicy::FailSilently;
            else if (Argument == "report") S.Policy = HTNCallTermErrorPolicy::Report;
            else Fail(inFile, LineNumber, "unknown policy " + Argument);
            S.Unit->GetExecutionContext().CallTermErrorPolicy = S.Policy;
        }
        else if (Command == "daemon")
        {
            if (Argument != "on" && Argument != "off")
                Fail(inFile, LineNumber, "daemon on|off");
            (void)S.Hook->GetCallTermBindingContext().SetDaemon("agent", Argument == "on" ? &S.Agent : nullptr);
        }
        else if (Command == "rebind")
        {
            const std::vector<std::string> Parts = Split(Argument, ' ');
            if (Parts.size() != 2u)
                Fail(inFile, LineNumber, "rebind <name> <int>");
            const std::string Name = Parts[0];
            const int32 Value = static_cast<int32>(std::stol(Parts[1]));
            S.Registry->Bind(Name, [Name, Value](const HTNCallTermArguments& inArguments) -> HTNAtomOwner {
                HTNAtomOwner Result(Value);
                Trace(Name, inArguments, Result);
                return Result;
            });
        }
        else if (Command == "call")
        {
            Emit("call " + Argument);
            HTNAtom Call;
            if (!MakeCall(Argument, Call))
                Fail(inFile, LineNumber, "invalid call: " + Argument);
            const HTNDecompositionStatus Status = S.Unit->DecomposeTopLevelMethod(Call);
            HTNAtom_Destroy(&Call);
            Emit(std::string("status ") + StatusName(Status));
            EmitPlan(*S.Unit->GetLastDecomposition().GetResult().Get());
        }
        else if (Command == "resolve")
        {
            Emit("resolve");
            for (;;)
            {
                const HTNPrimitiveTaskResolution Resolution = S.Unit->ResolveCurrentPrimitiveTask();
                if (Resolution == HTNPrimitiveTaskResolution::TaskReady)
                {
                    const HTNAtomOwner* Task = S.Unit->GetCurrentPrimitiveTask();
                    Emit("exec " + FormatStep(Task->Get()));
                    S.Unit->CompleteCurrentPrimitiveTask();
                    continue;
                }
                Emit(Resolution == HTNPrimitiveTaskResolution::PlanCompleted ? "resolution completed" : "resolution failed");
                break;
            }
        }
        else if (Command == "raw" || Command == "rawdeferred")
        {
            Emit(Command + " " + Argument);
            RunRaw(S, Argument, Command == "raw", inFile, LineNumber);
        }
        else if (Command == "end")
        {
            Finish();
        }
        else
        {
            Fail(inFile, LineNumber, "unknown command " + Command);
        }
    }
    Finish();
}
} // namespace

int main(int argc, char** argv)
{
    if (argc < 2)
    {
        std::fprintf(stderr, "usage: htn-oracle <scenario-file>...\n");
        return 1;
    }
    for (int Index = 1; Index < argc; ++Index)
    {
        RunFile(argv[Index]);
        std::fwrite(GOutput.data(), 1, GOutput.size(), stdout);
        GOutput.clear();
    }
    return 0;
}
